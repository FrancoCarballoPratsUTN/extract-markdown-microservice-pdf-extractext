package pdf

import (
	"context"
	"strings"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
)

type pdfiumExtractor struct {
	pool pdfium.Pool
}

// NewExtractor returns the PDFium-backed adapter for the usecase.Extractor
// port. The concrete type is returned so the business layer depends on the
// port it declares rather than on this package.
func NewExtractor(pool pdfium.Pool) *pdfiumExtractor {
	return &pdfiumExtractor{pool: pool}
}

func (e *pdfiumExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	if len(data) == 0 {
		return "", 0, domain.ErrInvalidPDF
	}

	pdfiumInstance, err := e.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return "", 0, domain.ErrExtractionFailed
	}
	defer pdfiumInstance.Close()

	doc, err := pdfiumInstance.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		return "", 0, domain.ErrInvalidPDF
	}
	defer pdfiumInstance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{
		Document: doc.Document,
	})

	pageCountResp, err := pdfiumInstance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	if err != nil {
		return "", 0, domain.ErrExtractionFailed
	}
	pageCount := pageCountResp.PageCount

	var builder strings.Builder
	for i := 0; i < pageCount; i++ {
		pageText, _ := extractPageText(pdfiumInstance, doc.Document, i)
		if pageText != "" {
			builder.WriteString(pageText)
			if i < pageCount-1 {
				builder.WriteString("\n\n")
			}
		}
	}

	return builder.String(), pageCount, nil
}

func extractPageText(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT, pageIndex int) (string, error) {
	textResp, err := inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: doc,
				Index:    pageIndex,
			},
		},
	})
	if err != nil {
		return "", err
	}
	defer inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{
		TextPage: textResp.TextPage,
	})

	charCountResp, err := inst.FPDFText_CountChars(&requests.FPDFText_CountChars{
		TextPage: textResp.TextPage,
	})
	if err != nil || charCountResp.Count <= 0 {
		return "", nil
	}

	getResp, err := inst.FPDFText_GetText(&requests.FPDFText_GetText{
		TextPage:   textResp.TextPage,
		StartIndex: 0,
		Count:      charCountResp.Count,
	})
	if err != nil {
		return "", err
	}

	return getResp.Text, nil
}
