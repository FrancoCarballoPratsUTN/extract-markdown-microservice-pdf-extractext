package usecase

import (
	"context"
	"errors"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

// queuedExtractor turns the pool's asynchronous submit/wait pair into the
// synchronous Extract port that the HTTP layer consumes. Keeping the queue
// behind the port means the handler stays unaware of admission control.
type queuedExtractor struct {
	pool *WorkerPool
}

// NewQueuedExtractor adapts a WorkerPool to the Extract port.
func NewQueuedExtractor(pool *WorkerPool) ExtractUsecase {
	return &queuedExtractor{pool: pool}
}

func (q *queuedExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	result, err := q.pool.Submit(data)
	if err != nil {
		// A full queue means this replica is already saturated. The caller
		// surfaces it as 503 so the client can retry instead of seeing a 500
		// for a condition that is not its fault.
		if errors.Is(err, ErrQueueFull) {
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, domain.ErrExtractionFailed
	}

	resp, err := q.pool.Wait(ctx, result)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, err
	}
	return resp, nil
}
