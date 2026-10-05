package domain

import "errors"

var (
	ErrInvalidPDF         = errors.New("invalid pdf")
	ErrTooLarge           = errors.New("pdf too large")
	ErrServiceUnavailable = errors.New("service unavailable")
	ErrExtractionFailed   = errors.New("extraction failed")
)

type ExtractResponse struct {
	Content   string `json:"content"`
	PageCount int    `json:"page_count"`
}
