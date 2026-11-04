package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// queuedExtractor turns the pool's asynchronous submit/wait pair into the
// synchronous Extract port that the HTTP layer consumes. Keeping the queue
// behind the port means the handler stays unaware of admission control.
type queuedExtractor struct {
	pool   *WorkerPool
	logger *slog.Logger
}

// NewQueuedExtractor adapts a WorkerPool to the Extract port.
func NewQueuedExtractor(pool *WorkerPool, logger *slog.Logger) ExtractUsecase {
	if logger == nil {
		logger = slog.Default()
	}
	return &queuedExtractor{pool: pool, logger: logger}
}

func (q *queuedExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	result, err := q.pool.Submit(ctx, data)
	if err != nil {
		// A full queue means this replica is already saturated. The caller
		// surfaces it as 503 so the client can retry instead of seeing a 500
		// for a condition that is not its fault. A closed pool is the same
		// situation during the shutdown: the work is not lost, it is refused.
		if errors.Is(err, ErrQueueFull) || errors.Is(err, ErrClosed) {
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, domain.ErrExtractionFailed
	}

	resp, err := q.pool.Wait(ctx, result)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// The client gave up (or the deadline expired) while the job was
			// still running. The job itself is not cancelled: it keeps running
			// on its own context and its result is discarded.
			requestID := reqctx.RequestID(ctx)
			if requestID == "" {
				q.logger.Warn("extraction abandoned before completion", "reason", err.Error())
			} else {
				q.logger.Warn("extraction abandoned before completion",
					"reason", err.Error(), "request_id", requestID)
			}
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, err
	}
	return resp, nil
}
