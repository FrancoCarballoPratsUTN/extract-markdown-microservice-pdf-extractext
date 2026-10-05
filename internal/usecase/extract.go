package usecase

import (
	"context"
	"errors"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
)

type ExtractUsecase interface {
	Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error)
}

type extractUsecase struct {
	extractor pdf.Extractor
}

func NewExtractUsecase(extractor pdf.Extractor) ExtractUsecase {
	return &extractUsecase{extractor: extractor}
}

func (uc *extractUsecase) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	if len(data) == 0 {
		return domain.ExtractResponse{}, domain.ErrInvalidPDF
	}

	content, pageCount, err := uc.extractor.Extract(ctx, data)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidPDF) {
			return domain.ExtractResponse{}, domain.ErrInvalidPDF
		}
		return domain.ExtractResponse{}, domain.ErrExtractionFailed
	}

	return domain.ExtractResponse{
		Content:   content,
		PageCount: pageCount,
	}, nil
}
