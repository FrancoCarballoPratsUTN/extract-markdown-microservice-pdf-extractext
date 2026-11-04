package http

import (
	"context"
	"log/slog"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// logDecision registra la decisión de un handler (rechazo de validación o fallo
// upstream). El nivel se
// deriva del status con la misma regla del access log: 5xx => ERROR, 4xx =>
// WARN, el resto => INFO. Nunca registra el contenido del PDF.
func logDecision(ctx context.Context, logger *slog.Logger, msg string, status int, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	args := make([]any, 0, len(attrs)+2)
	args = append(args, "status", status)
	if requestID := reqctx.RequestID(ctx); requestID != "" {
		args = append(args, "request_id", requestID)
	}
	args = append(args, attrs...)
	logger.Log(ctx, accessLogLevel(status), msg, args...)
}
