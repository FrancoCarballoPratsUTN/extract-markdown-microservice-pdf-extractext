package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// gatedExtractor blocks every extraction until gate closes, which lets a test
// hold jobs in the pool and observe admission behaviour deterministically.
type gatedExtractor struct {
	gate chan struct{}
}

func (g *gatedExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	select {
	case <-g.gate:
		return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// newGatedPool returns a pool whose workers are all blocked on an unclosed gate,
// an idempotent func that opens it, and a cleanup that always opens it before
// Close waits for the workers.
func newGatedPool(t *testing.T, workers, queueSize int, maxQueuedBytes int64) (*WorkerPool, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }

	wp := NewWorkerPool(&gatedExtractor{gate: gate}, workers, queueSize, maxQueuedBytes)
	t.Cleanup(wp.Close)
	t.Cleanup(open)
	return wp, open
}

func drain(t *testing.T, ch chan JobResult) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("job never completed")
	}
}

// A burst larger than the worker count must wait in the queue instead of being
// rejected: that is the whole reason the queue exists.
func TestWorkerPoolAbsorbsBurstBeyondWorkerCount(t *testing.T) {
	wp, open := newGatedPool(t, 1, 8, 1<<20)

	channels := make([]chan JobResult, 0, 4)
	for i := range 4 {
		ch, err := wp.Submit([]byte("hola"))
		if err != nil {
			t.Fatalf("submit %d rejected with %v, want the queue to absorb it", i, err)
		}
		channels = append(channels, ch)
	}

	open()
	for _, ch := range channels {
		drain(t, ch)
	}
}

// The byte budget is what keeps a queue of large PDFs inside the container's
// memory limit, so it has to reject before the process is OOM-killed.
func TestWorkerPoolRejectsWhenByteBudgetExhausted(t *testing.T) {
	wp, _ := newGatedPool(t, 1, 8, 16) // room for exactly two 8-byte payloads

	if _, err := wp.Submit(make([]byte, 8)); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := wp.Submit(make([]byte, 8)); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	_, err := wp.Submit(make([]byte, 8))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull once the byte budget is spent", err)
	}
}

// Bytes belong to a job's lifetime, not to the queue's: a job that finished
// extraction must hand its budget back or the service wedges after a while.
func TestWorkerPoolReleasesBytesAfterExtraction(t *testing.T) {
	wp, open := newGatedPool(t, 1, 8, 16)

	ch, err := wp.Submit(make([]byte, 8))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	open()
	drain(t, ch)

	if _, err := wp.Submit(make([]byte, 8)); err != nil {
		t.Fatalf("submit after completion: %v, want the freed budget to be reusable", err)
	}
}

// A client that gives up must not leave its handler blocked forever on a result
// that nobody will read.
func TestWorkerPoolExtractTimesOutInsteadOfHanging(t *testing.T) {
	wp, _ := newGatedPool(t, 1, 8, 1<<20)

	ch, err := wp.Submit([]byte("hola"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := wp.Wait(ctx, ch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait blocked for %v, want it to give up on the deadline", elapsed)
	}
}
