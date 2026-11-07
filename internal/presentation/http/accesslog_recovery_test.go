package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

// newRecordingLogger devuelve un logger que escribe registros JSON en el buffer
// recibido.
func newRecordingLogger(buffer *bytes.Buffer) *slog.Logger {
	return logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: buffer})
}

func TestWithRequestIDPreservesOrGeneratesResponseHeader(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantValue string
	}{
		{name: "preserves orchestrator ID", header: "orchestrator-123", wantValue: "orchestrator-123"},
		{name: "generates missing ID", wantValue: "generated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if reqctx.RequestID(r.Context()) == "" {
					t.Fatal("request ID missing from context")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if test.header != "" {
				request.Header.Set("X-Request-ID", test.header)
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			got := response.Header().Get("X-Request-ID")
			if test.wantValue == "generated" {
				if got == "" || got == test.header {
					t.Fatalf("generated request ID = %q", got)
				}
				return
			}
			if got != test.wantValue {
				t.Fatalf("response request ID = %q, want %q", got, test.wantValue)
			}
		})
	}
}

func TestWithRequestIDReplacesAnInvalidHeader(t *testing.T) {
	tests := []string{"bad id", "a\nb", strings.Repeat("x", 129)}

	for _, invalidHeader := range tests {
		t.Run(invalidHeader, func(t *testing.T) {
			var output bytes.Buffer
			logger := newRecordingLogger(&output)
			handler := withRequestID(withRequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := reqctx.RequestID(r.Context()); got == invalidHeader || !reqctx.IsValid(got) {
					t.Fatalf("context request ID = %q, want a valid replacement", got)
				}
				w.WriteHeader(http.StatusNoContent)
			})))
			request := httptest.NewRequest(http.MethodGet, "/extract", nil)
			request.Header.Set("X-Request-ID", invalidHeader)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if got := response.Header().Get("X-Request-ID"); got == invalidHeader || !reqctx.IsValid(got) {
				t.Fatalf("response request ID = %q, want a valid replacement", got)
			}
			if strings.Contains(output.String(), invalidHeader) {
				t.Fatalf("logs contain the invalid request ID %q: %s", invalidHeader, output.String())
			}
		})
	}
}

func TestRoutesCorrelatesRequestIDInResponseAndAccessLog(t *testing.T) {
	var output bytes.Buffer
	handler := Routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := reqctx.RequestID(r.Context()); got != "orchestrator-123" {
			t.Errorf("context request ID = %q, want orchestrator-123", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}), newRecordingLogger(&output))

	request := httptest.NewRequest(http.MethodPost, "/extract", nil)
	request.Header.Set("X-Request-ID", "orchestrator-123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); got != "orchestrator-123" {
		t.Fatalf("response request ID = %q, want orchestrator-123", got)
	}
	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Fatalf("access log missing request ID: %s", output.String())
	}
}

// El access log es la traza de auditoría del servicio: debe registrar el
// resultado de la petición sin repetir nunca lo que envió el cliente.
func TestWithRequestLogRecordsTheRequestOutcome(t *testing.T) {
	var output bytes.Buffer
	handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("body"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("payload")))

	record := decodeLogLine(t, output.Bytes())
	if record["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", record["method"])
	}
	if record["path"] != "/extract" {
		t.Errorf("path = %v, want /extract", record["path"])
	}
	if record["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want %d", record["status"], http.StatusCreated)
	}
	if _, ok := record["duration_ms"]; !ok {
		t.Error("the access log has no duration_ms field")
	}
	if strings.Contains(output.String(), "payload") {
		t.Errorf("the access log leaked the request body: %s", output.String())
	}
}

// El nivel del access log sigue al status: un 5xx es culpa nuestra, un 4xx del
// llamador y el resto es tráfico normal.
func TestAccessLogLevelFollowsStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		level  string
	}{
		{"200 is info", http.StatusOK, "INFO"},
		{"400 is warn", http.StatusBadRequest, "WARN"},
		{"404 is warn", http.StatusNotFound, "WARN"},
		{"500 is error", http.StatusInternalServerError, "ERROR"},
		{"503 is error", http.StatusServiceUnavailable, "ERROR"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))

			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/extract", nil))

			record := decodeLogLine(t, output.Bytes())
			if record["level"] != tc.level {
				t.Errorf("level = %v, want %s", record["level"], tc.level)
			}
		})
	}
}

// Un panic es un defecto, no una razón para perder el proceso: se convierte en
// un 500 y deja un registro con el stack.
func TestWithRecoveryTurnsAPanicIntoA500(t *testing.T) {
	var output bytes.Buffer
	handler := withRecovery(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	record := decodeLogLine(t, output.Bytes())
	if record["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", record["level"])
	}
	if record["panic"] != "boom" {
		t.Errorf("panic = %v, want %q", record["panic"], "boom")
	}
	if _, ok := record["stack"]; !ok {
		t.Error("the panic record has no stack")
	}
}

// Un panic también es una petición: tiene que aparecer en el access log con su
// 500, o la traza de auditoría del servicio perdería justo los fallos.
func TestAccessLogRecordsAPanickingRequest(t *testing.T) {
	var output bytes.Buffer
	logger := newRecordingLogger(&output)
	router := Routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	records := decodeLogLines(t, output.Bytes())
	access := findLogRecord(t, records, "http request")
	if access["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("access log status = %v, want %d", access["status"], http.StatusInternalServerError)
	}
}

// Una respuesta que ya empezó no se puede reescribir: un segundo documento
// dejaría al cliente con un body corrupto y un status que miente.
func TestRecoveryDoesNotCorruptAResponseThatAlreadyStarted(t *testing.T) {
	var output bytes.Buffer
	logger := newRecordingLogger(&output)
	handler := withRequestLog(logger)(withRecovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":"partial`))
		panic("boom")
	})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("the body was corrupted by a second document: %s", rec.Body.String())
	}
}

// El valor del panic y el stack vienen del proceso, no de un cliente, pero
// igualmente deben acotarse: un stack son kilobytes y un valor de panic puede
// llevar los datos que lo provocaron.
func TestThePanicRecordBoundsTheValueAndTheStack(t *testing.T) {
	var output bytes.Buffer
	handler := withRecovery(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(strings.Repeat("x", logging.MaxValueLength*2))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/extract", nil))

	record := decodeLogLine(t, output.Bytes())
	value, _ := record["panic"].(string)
	if got := len([]rune(value)); got != logging.MaxValueLength+1 {
		t.Errorf("the panic value has %d runes, want it truncated to %d", got, logging.MaxValueLength+1)
	}
	stack, _ := record["stack"].(string)
	if len(stack) > logging.MaxStackLength+1 {
		t.Errorf("the stack has %d bytes, want it bounded to %d", len(stack), logging.MaxStackLength)
	}
}

// El orquestador consulta la sonda de vida varias veces por segundo:
// registrarla enterraría las líneas que importan.
func TestAccessLogSkipsTheHealthProbe(t *testing.T) {
	var output bytes.Buffer
	handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if output.Len() != 0 {
		t.Errorf("the health probe produced a log line: %s", output.String())
	}
}

// El sentido del log es ser útil sin ser peligroso: el contenido del PDF nunca
// debe llegar a stdout.
//
// El test recorre el pipeline real (pool, extractor encolado, handler y router)
// con un extractor que devuelve una cadena reconocible: un handler de prueba
// nunca pondría el documento en el camino que podría filtrarlo.
func TestThePipelineNeverLogsTheDocumentContent(t *testing.T) {
	const secret = "SECRET-PDF-CONTENT"

	var output bytes.Buffer
	logger := newRecordingLogger(&output)

	pool := usecase.NewWorkerPool(
		usecase.NewExtractUsecase(secretExtractor{content: secret}),
		1, 1, 1<<20, logger,
	)
	t.Cleanup(pool.Close)

	router := Routes(NewHandler(usecase.NewQueuedExtractor(pool, logger), 1, logger), logger)

	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 "+secret))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	// La extracción ocurrió de verdad: sin esta aserción el test pasaría incluso
	// si el pipeline se hubiera cortocircuitado.
	if !strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("the response has no extracted content: %s", rec.Body.String())
	}
	if strings.Contains(output.String(), secret) {
		t.Errorf("the log leaked the document content: %s", output.String())
	}
}

// secretExtractor devuelve un contenido fijo, sea cual sea el documento.
type secretExtractor struct {
	content string
}

func (e secretExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	return e.content, 1, nil
}
