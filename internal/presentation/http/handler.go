package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// ExtractUsecase es el puerto que consume la capa HTTP.
type ExtractUsecase interface {
	Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error)
}

// Handler atiende POST /extract: valida el payload y delega la extracción.
type Handler struct {
	uc       ExtractUsecase
	maxBytes int64
	logger   *slog.Logger
}

// NewHandler construye el handler de extracción con el límite de subida en
// megabytes indicado.
func NewHandler(uc ExtractUsecase, maxUploadMB int, logger *slog.Logger) *Handler {
	return &Handler{
		uc:       uc,
		maxBytes: int64(maxUploadMB) << 20,
		logger:   logger,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		logDecision(r.Context(), h.logger, "extract rejected: method not allowed", http.StatusMethodNotAllowed)
		domain.WriteProblem(w, domain.NewProblem(http.StatusMethodNotAllowed, "Method Not Allowed", "method not allowed"))
		return
	}

	data, err := readPDFPayload(r, h.maxBytes)
	if err != nil {
		h.writePayloadError(w, r, err)
		return
	}

	resp, err := h.uc.Extract(r.Context(), data)
	if err != nil {
		h.writeExtractionError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writePayloadError traduce un fallo de lectura del body a su status. El
// detalle que vuelve al cliente es genérico: el error crudo puede describir
// el transporte y no le aporta nada al llamador.
func (h *Handler) writePayloadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrPayloadTooLarge):
		logDecision(r.Context(), h.logger, "pdf rejected: body exceeds size limit", http.StatusRequestEntityTooLarge)
		domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf file exceeds maximum size"))
	case errors.Is(err, ErrUnreadableBody):
		logDecision(r.Context(), h.logger, "pdf rejected: could not read request body", http.StatusBadRequest, "error", err.Error())
		domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", "could not read request body"))
	case errors.Is(err, ErrMissingFile), errors.Is(err, ErrUnsupportedMediaType):
		logDecision(r.Context(), h.logger, "pdf rejected: invalid request", http.StatusBadRequest, "error", err.Error())
		domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
	default:
		logDecision(r.Context(), h.logger, "pdf rejected: invalid request", http.StatusBadRequest)
		domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", "invalid request"))
	}
}

// writeExtractionError traduce un fallo de la extracción a su status.
func (h *Handler) writeExtractionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidPDF):
		logDecision(r.Context(), h.logger, "pdf rejected: malformed pdf", http.StatusBadRequest)
		domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
	case errors.Is(err, domain.ErrServiceUnavailable):
		logDecision(r.Context(), h.logger, "extraction unavailable", http.StatusServiceUnavailable)
		domain.WriteProblem(w, domain.NewProblem(http.StatusServiceUnavailable, "Service Unavailable", err.Error()))
	case errors.Is(err, domain.ErrTooLarge):
		logDecision(r.Context(), h.logger, "pdf rejected: exceeds page limit", http.StatusRequestEntityTooLarge)
		domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf exceeds maximum page count"))
	case errors.Is(err, domain.ErrExtractionFailed):
		logDecision(r.Context(), h.logger, "extraction failed", http.StatusInternalServerError, "error", err.Error())
		domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", err.Error()))
	default:
		logDecision(r.Context(), h.logger, "extraction failed", http.StatusInternalServerError)
		domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", "extraction failed"))
	}
}
