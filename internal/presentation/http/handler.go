package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// ExtractUsecase is the port consumed by the HTTP layer.
type ExtractUsecase interface {
	Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error)
}

type handler struct {
	uc          ExtractUsecase
	maxUploadMB int
	maxBytes    func(mb int) int64
	logger      *slog.Logger
}

func NewHandler(uc ExtractUsecase, maxUploadMB int, logger *slog.Logger) *handler {
	return &handler{
		uc:          uc,
		maxUploadMB: maxUploadMB,
		logger:      logger,
		maxBytes: func(mb int) int64 {
			return int64(mb) << 20
		},
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if r.Method != http.MethodPost {
		logDecision(r.Context(), h.logger, "extract rejected: method not allowed", http.StatusMethodNotAllowed)
		domain.WriteProblem(w, domain.NewProblem(http.StatusMethodNotAllowed, "Method Not Allowed", "method not allowed"))
		return
	}

	maxBytes := h.maxBytes(h.maxUploadMB)
	data, err := readPDFPayload(r, maxBytes)
	if err != nil {
		switch {
		case errors.Is(err, ErrPayloadTooLarge):
			logDecision(r.Context(), h.logger, "pdf rejected: body exceeds size limit", http.StatusRequestEntityTooLarge)
			domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf file exceeds maximum size"))
			return
		case errors.Is(err, ErrMissingFile), errors.Is(err, ErrUnsupportedMediaType):
			logDecision(r.Context(), h.logger, "pdf rejected: invalid request", http.StatusBadRequest, "error", err.Error())
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
			return
		default:
			logDecision(r.Context(), h.logger, "pdf rejected: invalid request", http.StatusBadRequest)
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", "invalid request"))
			return
		}
	}

	resp, err := h.uc.Extract(r.Context(), data)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidPDF):
			logDecision(r.Context(), h.logger, "pdf rejected: malformed pdf", http.StatusBadRequest)
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
			return
		case errors.Is(err, domain.ErrServiceUnavailable):
			logDecision(r.Context(), h.logger, "extraction unavailable", http.StatusServiceUnavailable)
			domain.WriteProblem(w, domain.NewProblem(http.StatusServiceUnavailable, "Service Unavailable", err.Error()))
			return
		case errors.Is(err, domain.ErrTooLarge):
			logDecision(r.Context(), h.logger, "pdf rejected: exceeds page limit", http.StatusRequestEntityTooLarge)
			domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf exceeds maximum page count"))
			return
		case errors.Is(err, domain.ErrExtractionFailed):
			logDecision(r.Context(), h.logger, "extraction failed", http.StatusInternalServerError, "error", err.Error())
			domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", err.Error()))
			return
		default:
			logDecision(r.Context(), h.logger, "extraction failed", http.StatusInternalServerError)
			domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", "extraction failed"))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
