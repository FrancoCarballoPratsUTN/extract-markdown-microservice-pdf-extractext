package usecase

import (
	"context"
	"errors"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// Extractor es el puerto del que depende esta capa de negocio. Se declara aquí,
// en el lado del consumidor, para que la dependencia apunte hacia dentro: la
// capa de infraestructura lo implementa sin que la capa de negocio importe
// nunca un adaptador.
type Extractor interface {
	Extract(ctx context.Context, data []byte) (content string, pageCount int, err error)
}

// ExtractUsecase es el puerto que consume la capa HTTP para extraer documentos.
type ExtractUsecase interface {
	Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error)
}

type extractUsecase struct {
	extractor Extractor
}

// NewExtractUsecase construye el caso de uso de extracción a partir del puerto
// Extractor.
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
