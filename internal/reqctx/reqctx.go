package reqctx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"unicode"
)

const (
	maxRequestIDLength = 128
	generatedIDLength  = 36
)

type contextKey struct{}

func EnsureRequestID(ctx context.Context, candidate string) (context.Context, string) {
	candidate = strings.TrimSpace(candidate)
	if !IsValid(candidate) {
		candidate = newRequestID()
	}
	return context.WithValue(ctx, contextKey{}, candidate), candidate
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if !IsValid(requestID) {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, requestID)
}

func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(contextKey{}).(string)
	return requestID
}

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

func newRequestID() string {
	var randomBytes [16]byte
	_, _ = rand.Read(randomBytes[:])
	randomBytes[6] = (randomBytes[6] & 0x0f) | 0x40
	randomBytes[8] = (randomBytes[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(randomBytes[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] +
		"-" + encoded[16:20] + "-" + encoded[20:]
}
