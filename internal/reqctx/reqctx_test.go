package reqctx

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureRequestIDKeepsValidValue(t *testing.T) {
	ctx, requestID := EnsureRequestID(context.Background(), "orchestrator-123")

	if requestID != "orchestrator-123" {
		t.Fatalf("requestID = %q, want original value", requestID)
	}
	if got := RequestID(ctx); got != requestID {
		t.Fatalf("RequestID(ctx) = %q, want %q", got, requestID)
	}
}

func TestEnsureRequestIDGeneratesValueWhenMissing(t *testing.T) {
	_, requestID := EnsureRequestID(context.Background(), "")

	if requestID == "" {
		t.Fatal("requestID is empty")
	}
	if len(requestID) != generatedIDLength {
		t.Fatalf("requestID length = %d, want %d", len(requestID), generatedIDLength)
	}
}

func TestEnsureRequestIDReplacesInvalidValue(t *testing.T) {
	_, requestID := EnsureRequestID(context.Background(), strings.Repeat("x", maxRequestIDLength+1))

	if requestID == strings.Repeat("x", maxRequestIDLength+1) {
		t.Fatal("invalid request ID was preserved")
	}
	if !IsValid(requestID) {
		t.Fatalf("generated request ID %q is invalid", requestID)
	}
}

func TestRequestIDReturnsEmptyWhenContextHasNoValue(t *testing.T) {
	if got := RequestID(context.Background()); got != "" {
		t.Fatalf("RequestID(background) = %q, want empty", got)
	}
}
