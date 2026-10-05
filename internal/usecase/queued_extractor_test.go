package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

func TestQueuedExtractorReturnsTheExtractedDocument(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{content: "hola", pageCount: 3}), 2, 4, 1<<20)
	t.Cleanup(wp.Close)

	resp, err := NewQueuedExtractor(wp).Extract(context.Background(), []byte("pdf"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if resp.Content != "hola" || resp.PageCount != 3 {
		t.Fatalf("resp = %+v, want content %q over %d pages", resp, "hola", 3)
	}
}

// signallingExtractor reports that a worker picked the job up before blocking,
// so a test can fill the queue without racing the pool.
type signallingExtractor struct {
	gate    chan struct{}
	started chan struct{}
}

func (s *signallingExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
	select {
	case <-s.gate:
		return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// A full queue is a saturation signal, not a client mistake, so it has to reach
// the client as 503 and not as a generic 500.
func TestQueuedExtractorReportsAFullQueueAsServiceUnavailable(t *testing.T) {
	gate := make(chan struct{})
	// Buffered so a worker taking a later job cannot block on the send and
	// deadlock Close; the test only needs the first signal.
	extractor := &signallingExtractor{gate: gate, started: make(chan struct{}, 4)}
	wp := NewWorkerPool(extractor, 1, 1, 1<<20)
	t.Cleanup(wp.Close)

	// The blocker occupies the worker; waiting for the signal is what makes the
	// queue slot free for the filler deterministically.
	blocker, err := wp.Submit([]byte("bloqueante"))
	if err != nil {
		t.Fatalf("submit blocker: %v", err)
	}
	t.Cleanup(func() { drain(t, blocker) })
	// Registered last so t.Cleanup's LIFO order opens the gate before the
	// drain and Close, which would otherwise wait forever on the blocked worker.
	t.Cleanup(func() { close(gate) })

	select {
	case <-extractor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never picked up the blocking job")
	}

	if _, err := wp.Submit([]byte("ocupa la cola")); err != nil {
		t.Fatalf("submit queue filler: %v", err)
	}

	_, err = NewQueuedExtractor(wp).Extract(context.Background(), []byte("rechazado"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
}

// A job that never finishes within the request deadline has to surface as 503:
// the client should retry elsewhere, not receive a 500.
func TestQueuedExtractorReportsAnExpiredDeadlineAsServiceUnavailable(t *testing.T) {
	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 4, 1<<20)
	t.Cleanup(wp.Close)
	t.Cleanup(func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewQueuedExtractor(wp).Extract(ctx, []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Extract blocked for %v, want it to give up on the deadline", elapsed)
	}
}
