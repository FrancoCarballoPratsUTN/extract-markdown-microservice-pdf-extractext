package reqctx

import (
	"context"
	"log/slog"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// WithRequestID guarda el request_id en el contexto. Devuelve un nuevo
// contexto con el valor adjunto.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestID extrae el request_id del contexto. Retorna string vacío si no está
// presente o si el tipo es inválido.
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}

// Logger devuelve un logger con el campo request_id si está presente en el
// contexto. Si no hay request_id, retorna el logger original sin cambios.
func Logger(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if id := RequestID(ctx); id != "" {
		return logger.With("request_id", id)
	}
	return logger
}
