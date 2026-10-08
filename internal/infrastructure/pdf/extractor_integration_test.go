package pdf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

const (
	testdataDir   = "../../../tests/stress/pdfs"
	scrumGuidePDF = "2020-Scrum-Guide-Spanish-Latin-South-American.pdf"
	extractBudget = 60 * time.Second
)

// requireNativeLib skips the run when the pdf_oxide shared library path is
// unset: the purego backend dlopens it at runtime, so tests without the lib
// would fail instead of running meaningfully. The container test stage exports
// it; locally run: go run github.com/yfedoseev/pdf_oxide/go/cmd/install@v0.3.78 -shared
func requireNativeLib(t *testing.T) {
	t.Helper()
	if os.Getenv("PDF_OXIDE_LIB_PATH") == "" {
		t.Skip("PDF_OXIDE_LIB_PATH is unset: install the pdf_oxide shared library and export its path")
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func extractCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), extractBudget)
	t.Cleanup(cancel)
	return ctx
}

func TestExtractorReturnsMarkdownFromRealPDF(t *testing.T) {
	requireNativeLib(t)

	content, pageCount, err := NewExtractor(2000).Extract(extractCtx(t), readFixture(t, scrumGuidePDF))

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if pageCount != 16 {
		t.Errorf("pageCount = %d, want 16", pageCount)
	}
	if content == "" {
		t.Fatal("content is empty, want markdown")
	}

	hasHeading := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			hasHeading = true
			break
		}
	}
	if !hasHeading {
		t.Error("content has no markdown heading (# …)")
	}
	if strings.ContainsRune(content, '\r') {
		t.Error("content contains carriage returns")
	}
	for _, r := range content {
		if r < 0x20 && r != '\n' && r != '\t' {
			t.Errorf("content contains control character %#U", r)
			break
		}
	}
	if content != strings.TrimRight(content, " \t\n") {
		t.Errorf("content has trailing whitespace: %q", content[len(content)-10:])
	}
}

func TestExtractorRejectsCorruptPDF(t *testing.T) {
	requireNativeLib(t)

	garbage := []byte("garbage payload that is not a pdf at all")

	_, _, err := NewExtractor(2000).Extract(extractCtx(t), garbage)

	if !errors.Is(err, domain.ErrInvalidPDF) {
		t.Fatalf("Extract() error = %v, want ErrInvalidPDF", err)
	}
}
