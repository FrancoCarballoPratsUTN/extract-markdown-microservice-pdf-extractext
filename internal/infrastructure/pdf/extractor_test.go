package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
	pdfoxide "github.com/yfedoseev/pdf_oxide/go"
)

type fakeDocument struct {
	pageCount   int
	pageErr     error
	markdown    string
	markdownErr error
	panicValue  any
	closed      bool
}

func (f *fakeDocument) PageCount() (int, error) { return f.pageCount, f.pageErr }

func (f *fakeDocument) ToMarkdownAll() (string, error) {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	return f.markdown, f.markdownErr
}

func (f *fakeDocument) Close() error {
	f.closed = true
	return nil
}

func TestExtractRejectsEmptyPayloadWithoutOpeningDocument(t *testing.T) {
	opened := false
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			opened = true
			return &fakeDocument{}, nil
		},
	}

	content, pages, err := extractor.Extract(context.Background(), nil)

	if !errors.Is(err, domain.ErrInvalidPDF) {
		t.Fatalf("Extract() error = %v, want ErrInvalidPDF", err)
	}
	if content != "" || pages != 0 {
		t.Errorf("Extract() = (%q, %d), want zero values", content, pages)
	}
	if opened {
		t.Error("open was called for an empty payload")
	}
}

func TestExtractMapsOpenSentinelsToInvalidPDF(t *testing.T) {
	sentinels := map[string]error{
		"invalid format":     pdfoxide.ErrInvalidFormat,
		"parse error":        pdfoxide.ErrParseError,
		"document not found": pdfoxide.ErrDocumentNotFound,
		"invalid path":       pdfoxide.ErrInvalidPath,
	}

	for name, sentinel := range sentinels {
		t.Run(name, func(t *testing.T) {
			extractor := &extractor{
				logger:   logging.Discard(),
				maxPages: 10,
				open: func([]byte) (document, error) {
					return nil, fmt.Errorf("open failed: %w", sentinel)
				},
			}

			_, _, err := extractor.Extract(context.Background(), []byte("pdf"))

			if !errors.Is(err, domain.ErrInvalidPDF) {
				t.Fatalf("Extract() error = %v, want ErrInvalidPDF", err)
			}
		})
	}
}

func TestExtractMapsUnexpectedOpenFailureToExtractionFailed(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			return nil, errors.New("socket: connection reset")
		},
	}

	_, _, err := extractor.Extract(context.Background(), []byte("pdf"))

	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Fatalf("Extract() error = %v, want ErrExtractionFailed", err)
	}
}

func TestExtractFailsWhenPageCountFails(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageErr: errors.New("broken catalog")}, nil
		},
	}

	_, _, err := extractor.Extract(context.Background(), []byte("pdf"))

	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Fatalf("Extract() error = %v, want ErrExtractionFailed", err)
	}
}

func TestExtractRejectsDocumentExceedingPageLimit(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageCount: 11}, nil
		},
	}

	content, pages, err := extractor.Extract(context.Background(), []byte("pdf"))

	if !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("Extract() error = %v, want ErrTooLarge", err)
	}
	if content != "" || pages != 0 {
		t.Errorf("Extract() = (%q, %d), want zero values", content, pages)
	}
}

func TestExtractTreatsZeroPageLimitAsUnlimited(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 0,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageCount: 99999, markdown: "# ok"}, nil
		},
	}

	content, pages, err := extractor.Extract(context.Background(), []byte("pdf"))

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if pages != 99999 || content != "# ok" {
		t.Errorf("Extract() = (%q, %d), want (# ok, 99999)", content, pages)
	}
}

func TestExtractFailsWhenMarkdownConversionFails(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageCount: 3, markdownErr: errors.New("reading order failed")}, nil
		},
	}

	_, _, err := extractor.Extract(context.Background(), []byte("pdf"))

	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Fatalf("Extract() error = %v, want ErrExtractionFailed", err)
	}
}

func TestExtractRecoversFromPanicDuringConversion(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			panic("native library exploded")
		},
	}

	content, pages, err := extractor.Extract(context.Background(), []byte("pdf"))

	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Fatalf("Extract() error = %v, want ErrExtractionFailed", err)
	}
	if content != "" || pages != 0 {
		t.Errorf("Extract() = (%q, %d), want zero values", content, pages)
	}
}

func TestPanicLogCarriesRequestID(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	panicValue := strings.Repeat("x", logging.MaxValueLength*2)
	extractor := &extractor{
		logger:   logger,
		maxPages: 10,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageCount: 1, panicValue: panicValue}, nil
		},
	}
	ctx := reqctx.WithRequestID(context.Background(), "orchestrator-123")

	_, _, err := extractor.Extract(ctx, []byte("pdf"))

	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Fatalf("Extract() error = %v, want ErrExtractionFailed", err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode panic log: %v; output=%q", err, output.String())
	}
	if record["request_id"] != "orchestrator-123" {
		t.Errorf("request_id = %v, want %q", record["request_id"], "orchestrator-123")
	}
	if record["panic"] != logging.Truncate(panicValue, logging.MaxValueLength) {
		t.Errorf("panic = %v, want truncated panic value", record["panic"])
	}
	stack, ok := record["stack"].(string)
	if !ok || stack == "" {
		t.Error("panic log has no stack")
	}
}

func TestExtractReturnsMarkdownAndPageCountAndClosesDocument(t *testing.T) {
	doc := &fakeDocument{pageCount: 16, markdown: "# Guía de Scrum\n\nContenido."}
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 2000,
		open: func([]byte) (document, error) {
			return doc, nil
		},
	}

	content, pages, err := extractor.Extract(context.Background(), []byte("pdf"))

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if pages != 16 {
		t.Errorf("pages = %d, want 16", pages)
	}
	if content != "# Guía de Scrum\n\nContenido." {
		t.Errorf("content = %q, want markdown passthrough", content)
	}
	if !doc.closed {
		t.Error("document was not closed")
	}
}

func TestSanitizeNormalizesLineEndingsControlsAndTrailingWhitespace(t *testing.T) {
	cases := map[string]struct{ input, want string }{
		"crlf and bare cr":    {"windows\r\nand\rmac\r\n", "windows\nand\nmac"},
		"control characters":  {"a\u0000b\u0007c", "abc"},
		"tabs and newlines":   {"col1\tcol2\nnext", "col1\tcol2\nnext"},
		"trailing whitespace": {"# Título\n\n  ", "# Título"},
		"untouched":           {"# Título\n\npárrafo", "# Título\n\npárrafo"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := sanitize(tc.input)
			if got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractSanitizesMarkdownFromDocument(t *testing.T) {
	extractor := &extractor{
		logger:   logging.Discard(),
		maxPages: 10,
		open: func([]byte) (document, error) {
			return &fakeDocument{pageCount: 2, markdown: "# Título\r\nhola\u0000 mundo\r"}, nil
		},
	}

	content, _, err := extractor.Extract(context.Background(), []byte("pdf"))

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if strings.ContainsRune(content, '\r') {
		t.Errorf("content %q still contains carriage returns", content)
	}
	if content != "# Título\nhola mundo" {
		t.Errorf("content = %q, want sanitized markdown", content)
	}
}
