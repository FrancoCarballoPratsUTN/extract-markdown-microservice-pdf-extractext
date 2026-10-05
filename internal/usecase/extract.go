package usecase

import (
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
)

type ExtractUseCase interface {
	Extract(data []byte) (domain.ExtractResponse, error)
}

type extractUseCase struct {
	extractor pdf.Extractor
}

func NewExtractUseCase(extractor pdf.Extractor) ExtractUseCase {
	return &extractUseCase{extractor: extractor}
}

func (u *extractUseCase) Extract(data []byte) (domain.ExtractResponse, error) {
	if len(data) == 0 {
		return domain.ExtractResponse{}, domain.ErrInvalidPDF
	}

	resp, err := u.extractor.Extract(data)
	if err != nil {
		return domain.ExtractResponse{}, err
	}
	return resp, nil
}
