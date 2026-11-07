package reqctx

import (
	"context"
	"crypto/rand"
	"strings"
	"unicode"
)

const maxRequestIDLength = 128

type contextKey struct{}

// EnsureRequestID devuelve un contexto que contiene candidate cuando es válido;
// en caso contrario, genera y almacena un nuevo request ID.
func EnsureRequestID(ctx context.Context, candidate string) (context.Context, string) {
	candidate = strings.TrimSpace(candidate)
	if !IsValid(candidate) {
		candidate = newRequestID()
	}
	return context.WithValue(ctx, contextKey{}, candidate), candidate
}

// WithRequestID devuelve un contexto que contiene requestID cuando es válido
// tras recortar los espacios; un ID inválido o vacío deja ctx sin cambios.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if !IsValid(requestID) {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, requestID)
}

// RequestID devuelve el request ID almacenado en ctx, o una cadena vacía si no
// hay ninguno.
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(contextKey{}).(string)
	return requestID
}

// IsValid indica si requestID no está vacío, es ASCII, no contiene caracteres
// de control ni espacios y mide como máximo 128 bytes.
func IsValid(requestID string) bool {
	if requestID == "" || len(requestID) > maxRequestIDLength {
		return false
	}
	for _, character := range requestID {
		if character > unicode.MaxASCII || unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

// newRequestID genera un request ID opaco y aleatorio.
func newRequestID() string {
	return rand.Text()
}
