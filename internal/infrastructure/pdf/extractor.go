package pdf

import (
	"bytes"
	"fmt"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
)

type Extractor interface {
	Extract(data []byte) (domain.ExtractResponse, error)
}

type extractor struct {
	pool pdfium.Pool
}

func NewExtractor(pool pdfium.Pool) Extractor {
	return &extractor{pool: pool}
}

func (e *extractor) Extract(data []byte) (domain.ExtractResponse, error) {
	if len(data) == 0 {
		return domain.ExtractResponse{}, domain.ErrInvalidPDF
	}

	pdfiumInstance, err := e.pool.GetInstance()
	if err != nil {
		return domain.ExtractResponse{}, fmt.Errorf("%w: %v", domain.ErrExtractionFailed, err)
	}
	defer e.pool.ReleaseInstance(pdfiumInstance)

	reader := bytes.NewReader(data)
	openReq := &requests.OpenDocument{
		FileReader: reader,
	}

	doc, err := pdfiumInstance.OpenDocument(openReq)
	if err != nil {
		return domain.ExtractResponse{}, fmt.Errorf("%w: %v", domain.ErrInvalidPDF, err)
	}
	defer pdfiumInstance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{
		Document: doc.Document,
	})

	pageCount, err := pdfiumInstance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	if err != nil {
		return domain.ExtractResponse{}, fmt.Errorf("%w: %v", domain.ErrExtractionFailed, err)
	}

	var content bytes.Buffer
	for i := 0; i < pageCount; i++ {
		textReq := &requests.GetPageText{
			Document: doc.Document,
			Page:     i,
		}
		text, err := pdfiumInstance.GetPageText(textReq)
		if err != nil {
			return domain.ExtractResponse{}, fmt.Errorf("%w: %v", domain.ErrExtractionFailed, err)
		}
		content.WriteString(text.Text)
		if i < pageCount-1 {
			content.WriteString("\n\n")
		}
	}

	return domain.ExtractResponse{
		Content:   content.String(),
		PageCount: pageCount,
	}, nil
}
