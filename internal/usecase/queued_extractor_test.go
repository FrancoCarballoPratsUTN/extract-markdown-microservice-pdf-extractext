package usecase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

func TestQueuedExtractorReturnsTheExtractedDocument(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{content: "hola", pageCount: 3}), 2, 4, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)

	resp, err := NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("pdf"))
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
	wp := NewWorkerPool(extractor, 1, 1, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)

	// The blocker occupies the worker; waiting for the signal is what makes the
	// queue slot free for the filler deterministically.
	blocker, err := wp.Submit(context.Background(), []byte("bloqueante"))
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

	if _, err := wp.Submit(context.Background(), []byte("ocupa la cola")); err != nil {
		t.Fatalf("submit queue filler: %v", err)
	}

	_, err = NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("rechazado"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
}

// A pool that is shutting down refuses the work instead of running it: the
// client has to see a 503 it can retry elsewhere, not a 500.
func TestQueuedExtractorReportsAClosedPoolAsServiceUnavailable(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{}), 1, 1, 1<<20, logging.Discard())
	wp.Close()

	_, err := NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
}

// A job that never finishes within the request deadline has to surface as 503:
// the client should retry elsewhere, not receive a 500.
func TestQueuedExtractorReportsAnExpiredDeadlineAsServiceUnavailable(t *testing.T) {
	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 4, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)
	t.Cleanup(func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewQueuedExtractor(wp, logging.Discard()).Extract(ctx, []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Extract blocked for %v, want it to give up on the deadline", elapsed)
	}
}

func TestQueuedExtractorLogsTheRequestIDWhenTheRequestIsAbandoned(t *testing.T) {
	var output bytes.Buffer
	gate := make(chan struct{})
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 4, 1<<20, logger)
	t.Cleanup(wp.Close)
	t.Cleanup(func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = reqctx.WithRequestID(ctx, "orchestrator-123")

	_, err := NewQueuedExtractor(wp, logger).Extract(ctx, []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Fatalf("abandoned extraction log missing request ID: %s", output.String())
	}
}
