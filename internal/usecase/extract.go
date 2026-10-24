package usecase

import (
	"context"
	"errors"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// Extractor is the port this business layer depends on. It is declared here, on
// the consumer side, so the dependency points inward: the infrastructure layer
// implements it without the business layer ever importing an adapter.
type Extractor interface {
	Extract(ctx context.Context, data []byte) (content string, pageCount int, err error)
}

type ExtractUsecase interface {
	Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error)
}

type extractUsecase struct {
	extractor Extractor
}

func NewExtractUsecase(extractor Extractor) ExtractUsecase {
	return &extractUsecase{extractor: extractor}
}

func (uc *extractUsecase) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	if len(data) == 0 {
		return domain.ExtractResponse{}, domain.ErrInvalidPDF
	}

	content, pageCount, err := uc.extractor.Extract(ctx, data)
	if err != nil {
		// Los sentinels de dominio se propagan tal cual: el handler los
		// traduce a su status (413 al superar el tope de páginas, 400 a un
		// PDF inválido). Colapsarlos todos en ErrExtractionFailed haría que un
		// documento con demasiadas páginas se respondiera con un 500.
		switch {
		case errors.Is(err, domain.ErrInvalidPDF):
			return domain.ExtractResponse{}, domain.ErrInvalidPDF
		case errors.Is(err, domain.ErrTooLarge):
			return domain.ExtractResponse{}, domain.ErrTooLarge
		default:
			return domain.ExtractResponse{}, domain.ErrExtractionFailed
		}
	}

	return domain.ExtractResponse{
		Content:   content,
		PageCount: pageCount,
	}, nil
}
