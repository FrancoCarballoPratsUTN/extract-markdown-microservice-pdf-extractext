package pdf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"unicode"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	pdfoxide "github.com/yfedoseev/pdf_oxide/go"
)

// document is the read-side view of an opened PDF the extractor needs.
type document interface {
	PageCount() (int, error)
	// ToMarkdownAll is the engine's own Markdown converter.
	ToMarkdownAll() (string, error)
	Close() error
}

// extractor adapts the pdf_oxide Go binding to the usecase.Extractor port.
// The concrete type is unexported so the business layer depends on the port it
// declares rather than on this package.
type extractor struct {
	maxPages int
	logger   *slog.Logger
	open     func([]byte) (document, error)
}

// NewExtractor returns the pdf_oxide-backed adapter. A maxPages of 0 disables
// the per-document page limit.
func NewExtractor(maxPages int, logger *slog.Logger) *extractor {
	return &extractor{
		maxPages: maxPages,
		logger:   logger,
		open: func(data []byte) (document, error) {
			return pdfoxide.OpenFromBytes(data)
		},
	}
}

// log returns the extractor logger.
//
// It never returns nil: the panic handler below depends on it, y un logger
// nulo convertiría un panic recuperado en un segundo panic, que es justo lo
// que el recovery existe para evitar.
func (e *extractor) log(ctx context.Context) *slog.Logger {
	if e.logger == nil {
		return slog.Default()
	}
	return e.logger
}

// Extract converts a PDF payload into Markdown via the engine's own
// ToMarkdownAll. ctx is part of the port only: the native conversion cannot be
// cancelled mid-call and the worker pool runs it with context.Background(), so
// admission control and request deadlines upstream bound how long callers wait.
func (e *extractor) Extract(ctx context.Context, data []byte) (content string, pages int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			content, pages = "", 0
			err = domain.ErrExtractionFailed
			e.log(ctx).Error("panic recovered while extracting",
				"panic", logging.Truncate(fmt.Sprint(recovered), logging.MaxValueLength),
				"stack", logging.Truncate(string(debug.Stack()), logging.MaxStackLength),
			)
		}
	}()

	if len(data) == 0 {
		return "", 0, domain.ErrInvalidPDF
	}

	doc, err := e.open(data)
	if err != nil {
		return "", 0, mapOpenError(err)
	}
	defer func() { _ = doc.Close() }()

	pages, err = doc.PageCount()
	if err != nil {
		return "", 0, domain.ErrExtractionFailed
	}
	if e.maxPages > 0 && pages > e.maxPages {
		return "", 0, domain.ErrTooLarge
	}

	content, err = doc.ToMarkdownAll()
	if err != nil {
		return "", 0, domain.ErrExtractionFailed
	}

	return sanitize(content), pages, nil
}

// mapOpenError translates pdf_oxide open failures into domain errors: an
// unparseable payload is the caller's fault, anything else is ours.
func mapOpenError(err error) error {
	switch {
	case errors.Is(err, pdfoxide.ErrInvalidFormat),
		errors.Is(err, pdfoxide.ErrParseError),
		errors.Is(err, pdfoxide.ErrDocumentNotFound),
		errors.Is(err, pdfoxide.ErrInvalidPath):
		return domain.ErrInvalidPDF
	default:
		return domain.ErrExtractionFailed
	}
}

// sanitize normalizes the engine output so clients always receive plain
// LF-terminated Markdown: PDF extraction can emit bare CRs and control
// characters, and documents may end in stray whitespace.
func sanitize(markdown string) string {
	withLF := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(markdown)

	var b strings.Builder
	b.Grow(len(withLF))
	for _, r := range withLF {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimRightFunc(b.String(), unicode.IsSpace)
}
