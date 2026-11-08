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

// document es la vista de lectura de un PDF abierto que necesita el extractor.
type document interface {
	PageCount() (int, error)
	// ToMarkdownAll es el conversor a Markdown propio del motor.
	ToMarkdownAll() (string, error)
	Close() error
}

// extractor adapta el binding Go de pdf_oxide al puerto usecase.Extractor.
// El tipo concreto es no exportado para que la capa de negocio dependa del
// puerto que declara y no de este paquete.
type extractor struct {
	maxPages int
	logger   *slog.Logger
	open     func([]byte) (document, error)
}

// NewExtractor devuelve el adaptador basado en pdf_oxide. Un maxPages de 0
// desactiva el límite de páginas por documento.
func NewExtractor(maxPages int, logger *slog.Logger) *extractor {
	return &extractor{
		maxPages: maxPages,
		logger:   logger,
		open: func(data []byte) (document, error) {
			return pdfoxide.OpenFromBytes(data)
		},
	}
}

// log devuelve el logger del extractor.
//
// Nunca devuelve nil: el manejador de panic de abajo depende de ello, y un
// logger nulo convertiría un panic recuperado en un segundo panic, que es justo
// lo que el recovery existe para evitar.
func (e *extractor) log(ctx context.Context) *slog.Logger {
	if e.logger == nil {
		return slog.Default()
	}
	return logging.WithRequestID(ctx, e.logger)
}

// Extract convierte un payload PDF en Markdown mediante ToMarkdownAll del
// propio motor. ctx forma parte del puerto sólo por la firma: la conversión
// nativa no se puede cancelar a mitad de llamada y el pool de workers la
// ejecuta con context.Background(), así que el control de admisión y los
// deadlines de las peticiones aguas arriba acotan cuánto esperan los llamadores.
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

// mapOpenError traduce los fallos de apertura de pdf_oxide a errores de
// dominio: un payload no parseable es culpa del llamador; cualquier otra cosa
// es nuestra.
func mapOpenError(err error) error {
	switch {
	case errors.Is(err, pdfoxide.ErrInvalidFormat),
		errors.Is(err, pdfoxide.ErrDocumentNotFound),
		errors.Is(err, pdfoxide.ErrInvalidPath):
		return domain.ErrInvalidPDF
	default:
		return domain.ErrExtractionFailed
	}
}

// sanitize normaliza la salida del motor para que los clientes reciban siempre
// Markdown plano terminado en LF: la extracción de PDF puede emitir CRs sueltos
// y caracteres de control, y los documentos pueden terminar con espacios
// sobrantes.
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
