package pdf

import (
	"context"
	"errors"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/klippa-app/go-pdfium"
)

// A nil pool must never be dereferenced: the usecase guards empty input before
// reaching the infrastructure layer, and the extractor rejects it defensively.
func TestExtractRejectsEmptyPayloadBeforeTouchingPool(t *testing.T) {
	extractor := NewExtractor(nil)

	content, pages, err := extractor.Extract(context.Background(), nil)

	if !errors.Is(err, domain.ErrInvalidPDF) {
		t.Fatalf("expected ErrInvalidPDF, got %v", err)
	}
	if content != "" || pages != 0 {
		t.Errorf("expected zero-value result, got content=%q pages=%d", content, pages)
	}
}

func TestNewExtractorAcceptsAnyPoolImplementation(t *testing.T) {
	var pool pdfium.Pool

	if extractor := NewExtractor(pool); extractor == nil {
		t.Fatal("expected extractor instance, got nil")
	}
}
