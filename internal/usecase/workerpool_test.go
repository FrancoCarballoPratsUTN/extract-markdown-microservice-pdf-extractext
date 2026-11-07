package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

type slowExtractor struct{}

func (s *slowExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	time.Sleep(20 * time.Millisecond)
	return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
}

func TestWorkerPoolProcessesJob(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{content: "hi", pageCount: 1})
	wp := NewWorkerPool(uc, 2, 10, 1<<20, logging.Discard())
	defer wp.Close()

	ch, err := wp.Submit(context.Background(), []byte("hi"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	select {
	case res := <-ch:
		if res.Err != nil {
			t.Fatalf("err: %v", res.Err)
		}
		if res.Resp.Content != "hi" {
			t.Fatalf("content")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

func TestWorkerPoolQueueFullWhenNoSpace(t *testing.T) {
	wp := NewWorkerPool(&slowExtractor{}, 1, 1, 1<<20, logging.Discard())
	defer wp.Close()

	if _, err := wp.Submit(context.Background(), []byte("a")); err != nil {
		t.Fatalf("1: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := wp.Submit(context.Background(), []byte("b")); err != nil {
		t.Fatalf("2: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err := wp.Submit(context.Background(), []byte("c"))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
}
