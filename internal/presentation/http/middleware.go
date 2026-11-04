package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// AdmissionMiddleware limita cuántas extracciones corren a la vez. Cuando el
// límite está alcanzado, la petición se rechaza con 503 en vez de esperar:
// una espera sin límite solo convertiría la saturación en latencia.
type AdmissionMiddleware struct {
	sem chan struct{}
}

func NewAdmissionMiddleware(maxConcurrent int) *AdmissionMiddleware {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &AdmissionMiddleware{
		sem: make(chan struct{}, maxConcurrent),
	}
}

func (m *AdmissionMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
			next.ServeHTTP(w, r)
		default:
			domain.WriteProblem(w, domain.NewProblem(http.StatusServiceUnavailable, "Service Unavailable", "server is saturated"))
		}
	})
}

// WithDeadline gives every request a context deadline of timeout. A queued
// extraction is bounded by that deadline instead of running until the client
// disconnects, which keeps the queue draining predictably under saturation.
func WithDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// maxMethodLength acota el método HTTP que se registra. Los métodos reales
// ("GET", "PROPFIND") no llegan al tope: sólo un cliente que los infla
// llega.
const maxMethodLength = 16

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, requestID := reqctx.EnsureRequestID(r.Context(), r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withRecovery convierte un panic en un 500 en vez de tumbar el proceso. El
// stack se registra porque un panic es un fallo real que hay que depurar, pero
// acotado para que una línea no crezca sin límite.
//
// El recovery va dentro del access log (ver Routes), así que un panic también
// queda registrado como una petición más: si no, el 500 sería invisible en el
// access log.
func withRecovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				logger.Error("panic recovered",
					"panic", logging.Truncate(fmt.Sprint(recovered), logging.MaxValueLength),
					"method", r.Method,
					"path", logging.Truncate(r.URL.Path, logging.MaxValueLength),
					"stack", logging.Truncate(string(debug.Stack()), logging.MaxStackLength),
					"request_id", reqctx.RequestID(r.Context()),
				)
				// Si la respuesta ya empezó, no se puede reescribir: un segundo
				// documento JSON dejaría al cliente con un cuerpo corrupto. En
				// ese caso sólo queda el log.
				if recorder, ok := w.(*statusRecorder); ok && recorder.status != 0 {
					return
				}
				domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", "internal server error"))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// withRequestLog escribe una línea por petición. Los campos son los de una
// lista blanca: método, ruta, status y duración. Nunca
// headers, nunca el cuerpo.
//
// La ruta y el método vienen del cliente, así que se acotan: sin un tope, un
// path largo convertiría la línea de log en un archivo.
func withRequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHealthRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)

			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Log(r.Context(), accessLogLevel(status), "http request",
				"method", logging.Truncate(r.Method, maxMethodLength),
				"path", logging.Truncate(r.URL.Path, logging.MaxValueLength),
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", reqctx.RequestID(r.Context()),
			)
		})
	}
}

// statusRecorder envuelve el ResponseWriter para capturar el status del access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush reenvía al writer subyacente si lo soporta, para no romper streaming.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// accessLogLevel deriva el nivel de la línea de acceso del status: un 5xx es
// un fallo del servicio, un 4xx un error del cliente, y el resto, tráfico
// normal.
func accessLogLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// isHealthRequest son las sondas que no pasan por el access log: se consultan
// con alta frecuencia y solo generarían ruido.
//
// El método importa: un POST a /healthz no es una sonda, es una petición
// equivocada que sí debe quedar registrada.
func isHealthRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/healthz"
}
