package usecase

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// ErrQueueFull reports that the bounded queue had no room for the job. Callers
// should surface it as 503 so the client can retry with backoff.
var ErrQueueFull = errors.New("queue full")

type JobResult struct {
	Resp domain.ExtractResponse
	Err  error
}

// job owns the channel its result is written to. One job belongs to exactly one
// submitter, which is what stops concurrent callers from receiving each other's
// documents.
type job struct {
	data   []byte
	size   int64
	result chan JobResult
}

// WorkerPool decouples HTTP request handling from the CPU-bound extraction. It
// bounds the number of extractions running, the number of jobs waiting, and the
// bytes those waiting jobs hold, so a burst costs latency rather than rejected
// requests or an OOM kill.
type WorkerPool struct {
	extractor ExtractUsecase
	jobs      chan job
	wg        sync.WaitGroup

	// budget is the admission byte cap shared by running and queued jobs: both
	// hold the whole PDF in memory until extraction returns.
	budget *byteBudget

	mu     sync.Mutex
	closed bool
}

// byteBudget is a counting semaphore for bytes. A plain int64 behind a mutex is
// enough: Submit runs once per request, so contention is irrelevant next to the
// extraction cost it guards.
type byteBudget struct {
	mu    sync.Mutex
	limit int64
	used  int64
}

// tryAcquire reserves size bytes, reporting false when the budget is spent.
func (b *byteBudget) tryAcquire(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+size > b.limit {
		return false
	}
	b.used += size
	return true
}

// release returns size bytes to the budget.
func (b *byteBudget) release(size int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= size
}

func NewWorkerPool(extractor ExtractUsecase, workers, queueSize int, maxQueuedBytes int64) *WorkerPool {
	if workers <= 0 {
		workers = 1
	}
	// The queue exists so Submit can hand off without blocking. That requires
	// room for one job per worker: with a smaller buffer a non-blocking send
	// would fail even though a worker sits idle, rejecting work the pool could
	// have started immediately.
	if queueSize < workers {
		queueSize = workers
	}
	if maxQueuedBytes <= 0 {
		maxQueuedBytes = math.MaxInt64
	}
	wp := &WorkerPool{
		extractor: extractor,
		jobs:      make(chan job, queueSize),
		budget:    &byteBudget{limit: maxQueuedBytes},
	}
	for i := 0; i < workers; i++ {
		wp.wg.Add(1)
		go wp.work()
	}
	return wp
}

func (wp *WorkerPool) work() {
	defer wp.wg.Done()
	for j := range wp.jobs {
		resp, err := wp.extractor.Extract(context.Background(), j.data)
		// The PDF is unreachable from here on, so give its budget back before
		// handing off the result: that lets the next burst in immediately.
		wp.budget.release(int64(len(j.data)))
		j.result <- JobResult{Resp: resp, Err: err}
	}
}

// Submit enqueues data and returns the channel that carries its result. The
// channel is buffered so a worker never blocks handing off, even if the caller
// gave up waiting.
func (wp *WorkerPool) Submit(data []byte) (chan JobResult, error) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.closed {
		return nil, errors.New("closed")
	}

	size := int64(len(data))
	if !wp.budget.tryAcquire(size) {
		return nil, ErrQueueFull
	}

	j := job{data: data, size: size, result: make(chan JobResult, 1)}
	select {
	case wp.jobs <- j:
		return j.result, nil
	default:
		wp.budget.release(size)
		return nil, ErrQueueFull
	}
}

// Wait blocks for a submitted job's result, or returns the context error if the
// deadline passes first. The HTTP write timeout is what supplies that deadline,
// so a client that walks away leaves the job queued instead of pinning a handler.
func (wp *WorkerPool) Wait(ctx context.Context, ch chan JobResult) (domain.ExtractResponse, error) {
	select {
	case res := <-ch:
		return res.Resp, res.Err
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// Close stops accepting work and waits for the jobs already queued to finish.
func (wp *WorkerPool) Close() {
	wp.mu.Lock()
	if !wp.closed {
		wp.closed = true
		close(wp.jobs)
	}
	wp.mu.Unlock()
	wp.wg.Wait()
}
