package http

import (
	"log/slog"
	"net/http"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// Routes compose el router del servicio.
//
// La sonda de vida vive fuera de los middlewares de admisión y de deadline que
// el composition root aplica a la ruta de extracción: un servicio saturado no
// está muerto, y una sonda que dependiera de la carga declararía caído al
// contenedor justo cuando menos falta hace.
//
// La cadena de observabilidad envuelve al router completo. El access log va
// fuera del recovery para que un panic también quede registrado como una
// petición más (con su 500): si el recovery envolviera al access log, el
// panic desenrollaría el access log y esa petición no aparecería en él.
func Routes(extract http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.Handle("/extract", extract)
	mux.HandleFunc("/", notFoundHandler)

	return withRequestLog(logger)(withRecovery(logger)(mux))
}

// healthHandler responde a la sonda de vida. Es estática: no consulta
// dependencias, así que un incidente en otro servicio no marca este como
// caído.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// notFoundHandler cierra el router: toda ruta desconocida devuelve el mismo
// documento de problema que el resto de errores, para que un cliente no
// tenga que parsear dos formatos distintos.
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	domain.WriteProblem(w, domain.NewProblem(http.StatusNotFound, "Not Found", "no route matches "+r.Method+" "+r.URL.Path))
}
