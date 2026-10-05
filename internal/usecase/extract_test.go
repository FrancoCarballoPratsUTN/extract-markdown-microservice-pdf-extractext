package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

type mockExtractor struct {
	content   string
	pageCount int
	err       error
}

func (m *mockExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	return m.content, m.pageCount, m.err
}

func TestExtractEmptyData(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{})
	_, err := uc.Extract(context.Background(), []byte{})
	if !errors.Is(err, domain.ErrInvalidPDF) {
		t.Errorf("expected ErrInvalidPDF, got %v", err)
	}
}

func TestExtractSuccess(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{content: "hello", pageCount: 2, err: nil})
	res, err := uc.Extract(context.Background(), []byte("pdf"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "hello" || res.PageCount != 2 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestExtractInvalidPDF(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{err: domain.ErrInvalidPDF})
	_, err := uc.Extract(context.Background(), []byte("pdf"))
	if !errors.Is(err, domain.ErrInvalidPDF) {
		t.Errorf("expected ErrInvalidPDF, got %v", err)
	}
}

func TestExtractFailureWrapped(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{err: errors.New("boom")})
	_, err := uc.Extract(context.Background(), []byte("pdf"))
	if !errors.Is(err, domain.ErrExtractionFailed) {
		t.Errorf("expected ErrExtractionFailed, got %v", err)
	}
}
