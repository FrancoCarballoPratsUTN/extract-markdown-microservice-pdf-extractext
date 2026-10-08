package http

import (
	"context"
	"encoding/json"
	"errors"
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
}

func NewHandler(uc ExtractUsecase, maxUploadMB int) *handler {
	return &handler{
		uc:          uc,
		maxUploadMB: maxUploadMB,
		maxBytes: func(mb int) int64 {
			return int64(mb) << 20
		},
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		domain.WriteProblem(w, domain.NewProblem(http.StatusMethodNotAllowed, "Method Not Allowed", "method not allowed"))
		return
	}

	maxBytes := h.maxBytes(h.maxUploadMB)
	data, err := readPDFPayload(r, maxBytes)
	if err != nil {
		switch {
		case errors.Is(err, ErrPayloadTooLarge):
			domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf file exceeds maximum size"))
			return
		case errors.Is(err, ErrMissingFile), errors.Is(err, ErrUnsupportedMediaType):
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
			return
		default:
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", "invalid request"))
			return
		}
	}

	resp, err := h.uc.Extract(r.Context(), data)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidPDF):
			domain.WriteProblem(w, domain.NewProblem(http.StatusBadRequest, "Bad Request", err.Error()))
			return
		case errors.Is(err, domain.ErrServiceUnavailable):
			domain.WriteProblem(w, domain.NewProblem(http.StatusServiceUnavailable, "Service Unavailable", err.Error()))
			return
		case errors.Is(err, domain.ErrTooLarge):
			domain.WriteProblem(w, domain.NewProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "pdf exceeds maximum page count"))
			return
		case errors.Is(err, domain.ErrExtractionFailed):
			domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", err.Error()))
			return
		default:
			domain.WriteProblem(w, domain.NewProblem(http.StatusInternalServerError, "Internal Server Error", "extraction failed"))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
