package http

import (
	"context"
	"log/slog"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// logDecision registra la decisión de un handler (rechazo de validación o fallo
// upstream) adjuntando el status HTTP y el request_id del contexto. El nivel se
// deriva del status: 5xx => ERROR, 4xx => WARN, el resto => INFO. Nunca registra
// el contenido del PDF.
func logDecision(ctx context.Context, logger *slog.Logger, msg string, status int, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelInfo
	switch {
	case status >= 500:
		level = slog.LevelError
	case status >= 400:
		level = slog.LevelWarn
	}
	args := make([]any, 0, len(attrs)+2)
	args = append(args, "status", status)
	args = append(args, attrs...)
	reqctx.Logger(ctx, logger).Log(ctx, level, msg, args...)
}
