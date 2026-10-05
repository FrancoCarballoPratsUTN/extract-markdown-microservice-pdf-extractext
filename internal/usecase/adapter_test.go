package usecase_test

import (
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

// The PDFium adapter must satisfy the port the business layer declares. This
// lives in an external test package so the conformance check can import the
// adapter without the production dependency pointing outward.
var _ usecase.Extractor = pdf.NewExtractor(nil)

func TestPDFiumAdapterIsUsableAsUsecaseDependency(t *testing.T) {
	var extractor usecase.Extractor = pdf.NewExtractor(nil)

	if extractor == nil {
		t.Fatal("expected a non-nil extractor")
	}
}
