package logging

import (
	"context"
	"log/slog"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// WithRequestID returns a logger that adds request_id when ctx contains one.
func WithRequestID(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if requestID := reqctx.RequestID(ctx); requestID != "" {
		return logger.With("request_id", requestID)
	}
	return logger
}
