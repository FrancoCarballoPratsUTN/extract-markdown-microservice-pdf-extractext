package domain

import "errors"

var (
	ErrInvalidPDF         = errors.New("invalid pdf")
	ErrTooLarge           = errors.New("pdf too large")
	ErrServiceUnavailable = errors.New("service unavailable")
	ErrExtractionFailed   = errors.New("extraction failed")
)

// ExtractResponse es la respuesta de una extracción correcta.
type ExtractResponse struct {
	Content   string `json:"content"`
	PageCount int    `json:"page_count"`
}
