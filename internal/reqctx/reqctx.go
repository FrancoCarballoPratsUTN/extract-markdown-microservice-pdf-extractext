package reqctx

import (
	"context"
	"crypto/rand"
	"strings"
	"unicode"
)

const maxRequestIDLength = 128

type contextKey struct{}

// EnsureRequestID returns a context containing candidate when it is valid,
// otherwise it generates and stores a new request ID.
func EnsureRequestID(ctx context.Context, candidate string) (context.Context, string) {
	candidate = strings.TrimSpace(candidate)
	if !IsValid(candidate) {
		candidate = newRequestID()
	}
	return context.WithValue(ctx, contextKey{}, candidate), candidate
}

// WithRequestID returns a context containing requestID when it is valid after
// trimming surrounding whitespace; invalid or empty IDs leave ctx unchanged.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if !IsValid(requestID) {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, requestID)
}

// RequestID returns the request ID stored in ctx, or an empty string when none
// is present.
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(contextKey{}).(string)
	return requestID
}

// IsValid reports whether requestID is non-empty, ASCII, non-control,
// non-whitespace, and at most 128 bytes long.
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

// newRequestID generates a random opaque request ID.
func newRequestID() string {
	return rand.Text()
}
