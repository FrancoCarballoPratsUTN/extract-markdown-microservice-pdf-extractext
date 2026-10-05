package usecase

import (
	"context"
	"errors"
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
	result chan JobResult
}

// WorkerPool decouples HTTP request handling from the CPU-bound extraction. It
// bounds both the number of extractions running and the number of jobs waiting,
// so a burst costs latency rather than rejected requests.
type WorkerPool struct {
	extractor ExtractUsecase
	jobs      chan job
	wg        sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

func NewWorkerPool(extractor ExtractUsecase, workers, queueSize int) *WorkerPool {
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
	wp := &WorkerPool{
		extractor: extractor,
		jobs:      make(chan job, queueSize),
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

	j := job{data: data, result: make(chan JobResult, 1)}
	select {
	case wp.jobs <- j:
		return j.result, nil
	default:
		return nil, ErrQueueFull
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