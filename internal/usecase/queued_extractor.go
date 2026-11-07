package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

// queuedExtractor convierte el par asíncrono submit/wait del pool en el puerto
// Extract síncrono que consume la capa HTTP. Mantener la cola detrás del puerto
// hace que el handler no sepa nada del control de admisión.
type queuedExtractor struct {
	pool   *WorkerPool
	logger *slog.Logger
}

// NewQueuedExtractor adapta un WorkerPool al puerto Extract.
func NewQueuedExtractor(pool *WorkerPool, logger *slog.Logger) ExtractUsecase {
	if logger == nil {
		logger = slog.Default()
	}
	return &queuedExtractor{pool: pool, logger: logger}
}

func (q *queuedExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	result, err := q.pool.Submit(ctx, data)
	if err != nil {
		// Una cola llena significa que esta réplica ya está saturada. El llamador
		// lo expone como 503 para que el cliente reintente en lugar de ver un 500
		// por una condición que no es culpa suya. Un pool cerrado es la misma
		// situación durante el apagado: el trabajo no se pierde, se rechaza.
		if errors.Is(err, ErrQueueFull) || errors.Is(err, ErrClosed) {
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, domain.ErrExtractionFailed
	}

	resp, err := q.pool.Wait(ctx, result)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// El cliente se rindió (o venció el deadline) mientras el trabajo
			// seguía en curso. El trabajo no se cancela: continúa con su propio
			// contexto y su resultado se descarta.
			logging.WithRequestID(ctx, q.logger).Warn("extraction abandoned before completion", "reason", err.Error())
			return domain.ExtractResponse{}, domain.ErrServiceUnavailable
		}
		return domain.ExtractResponse{}, err
	}
	return resp, nil
}
