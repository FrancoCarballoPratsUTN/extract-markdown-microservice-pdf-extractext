package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

type slowExtractor struct{}

func (s *slowExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	time.Sleep(20 * time.Millisecond)
	return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
}

type ctxKey struct{}

func TestWorkerPoolProcessesJob(t *testing.T) {
	uc := NewExtractUsecase(&mockExtractor{content: "hi", pageCount: 1})
	wp := NewWorkerPool(uc, 2, 10, 1<<20)
	defer wp.Close()

	ch, err := wp.Submit([]byte("hi"))
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

// Queue-full behaviour is covered by TestWorkerPoolQueueFullWhenNoSpace.

func TestWorkerPoolQueueFullWhenNoSpace(t *testing.T) {
	// workers 1, queue 1 - fill both: 1 job processing + 1 queued = full
	wp := NewWorkerPool(&slowExtractor{}, 1, 1, 1<<20)
	defer wp.Close()

	// First job: sent to buffer/worker
	if _, err := wp.Submit([]byte("a")); err != nil {
		t.Fatalf("1: %v", err)
	}
	// Give worker time to start and be busy
	time.Sleep(5 * time.Millisecond)
	// Second job should fit in queue buffer (size 1) while worker is busy
	if _, err := wp.Submit([]byte("b")); err != nil {
		t.Fatalf("2: %v", err)
	}
	// Give a bit more to ensure worker stays busy
	time.Sleep(5 * time.Millisecond)
	// Third job: buffer full and worker busy -> should be full
	_, err := wp.Submit([]byte("c"))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
}
