package usecase

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// ErrQueueFull reports that the bounded queue had no room for the job. Callers
// should surface it as 503 so the client can retry with backoff.
var ErrQueueFull = errors.New("queue full")

// ErrClosed reports that the pool no longer accepts work because it is
// shutting down.
var ErrClosed = errors.New("worker pool closed")

type JobResult struct {
	Resp domain.ExtractResponse
	Err  error
}

// job owns the channel its result is written to. One job belongs to exactly one
// submitter, which is what stops concurrent callers from receiving each other's
// documents.
type job struct {
	data      []byte
	result    chan JobResult
	requestID string
}

// WorkerPool decouples HTTP request handling from the CPU-bound extraction. It
// bounds the number of extractions running, the number of jobs waiting, and the
// bytes those waiting jobs hold, so a burst costs latency rather than rejected
// requests or an OOM kill.
type WorkerPool struct {
	extractor ExtractUsecase
	jobs      chan job
	wg        sync.WaitGroup
	logger    *slog.Logger

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

func NewWorkerPool(extractor ExtractUsecase, workers, queueSize int, maxQueuedBytes int64, logger *slog.Logger) *WorkerPool {
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
	if logger == nil {
		logger = slog.Default()
	}
	wp := &WorkerPool{
		extractor: extractor,
		jobs:      make(chan job, queueSize),
		budget:    &byteBudget{limit: maxQueuedBytes},
		logger:    logger,
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
		j.result <- wp.run(j)
	}
}

// run extracts one job and reports the outcome.
//
// The extraction runs on its own context because the native conversion cannot
// be cancelled mid-call: a client that walks away must not abort the work
// already in flight.
func (wp *WorkerPool) run(j job) JobResult {
	start := time.Now()
	extractionContext := reqctx.WithRequestID(context.Background(), j.requestID)
	logger := wp.logger
	if j.requestID != "" {
		logger = logger.With("request_id", j.requestID)
	}
	resp, err := wp.extractor.Extract(extractionContext, j.data)
	duration := time.Since(start).Milliseconds()

	// The PDF is unreachable from here on, so give its budget back before
	// handing off the result: that lets the next burst in immediately.
	wp.budget.release(int64(len(j.data)))

	if err != nil {
		logger.Error("extraction failed", "duration_ms", duration, "error", err)
		return JobResult{Err: err}
	}
	logger.Info("extraction completed", "pages", resp.PageCount, "duration_ms", duration)
	return JobResult{Resp: resp}
}

// Submit enqueues data and returns the channel that carries its result. The
// channel is buffered so a worker never blocks handing off, even if the caller
// gave up waiting.
//
// The job runs on its own context so a disconnected client does not cancel
// work in flight.
func (wp *WorkerPool) Submit(ctx context.Context, data []byte) (chan JobResult, error) {
	size := int64(len(data))

	wp.mu.Lock()
	if wp.closed {
		wp.mu.Unlock()
		return nil, ErrClosed
	}
	if !wp.budget.tryAcquire(size) {
		wp.mu.Unlock()
		wp.logRejection(ctx, size)
		return nil, ErrQueueFull
	}

	j := job{
		data:      data,
		result:    make(chan JobResult, 1),
		requestID: reqctx.RequestID(ctx),
	}
	select {
	case wp.jobs <- j:
		wp.mu.Unlock()
		return j.result, nil
	default:
		wp.budget.release(size)
		wp.mu.Unlock()
		wp.logRejection(ctx, size)
		return nil, ErrQueueFull
	}
}

// logRejection reports that a job could not be admitted. Both reasons mean the
// same thing to the caller: this replica is saturated.
func (wp *WorkerPool) logRejection(ctx context.Context, size int64) {
	requestID := reqctx.RequestID(ctx)
	if requestID == "" {
		wp.logger.Warn("queue full; rejecting job", "bytes", size)
		return
	}
	wp.logger.Warn("queue full; rejecting job", "bytes", size, "request_id", requestID)
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
	_ = wp.CloseContext(context.Background())
}

// CloseContext stops accepting work and waits for the queued jobs to finish,
// giving up when the context expires. A bounded drain keeps a stuck native call
// from blocking the shutdown forever.
//
// When the context expires the worker goroutine keeps running until the native
// call returns: the job still holds its PDF in memory. That is deliberate —
// the alternative is killing the process mid-conversion — and harmless here
// because the caller is shutting the process down anyway.
func (wp *WorkerPool) CloseContext(ctx context.Context) error {
	wp.mu.Lock()
	if !wp.closed {
		wp.closed = true
		close(wp.jobs)
	}
	wp.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
