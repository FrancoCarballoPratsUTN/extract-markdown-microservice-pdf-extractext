package reqctx

import (
	"context"
	"strings"
	"testing"
)

// Current rand.Text length; this is a minimum contract, not a stable format.
const wantGeneratedIDLength = 26

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
	if len(requestID) != wantGeneratedIDLength {
		t.Fatalf("requestID length = %d, want %d", len(requestID), wantGeneratedIDLength)
	}
	if !isBase32(requestID) {
		t.Fatalf("requestID %q is not RFC 4648 base32", requestID)
	}
	if !IsValid(requestID) {
		t.Fatalf("generated request ID %q is invalid", requestID)
	}

	_, anotherRequestID := EnsureRequestID(context.Background(), "")
	if requestID == anotherRequestID {
		t.Fatal("two generated request IDs are equal")
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

func TestWithRequestID(t *testing.T) {
	tests := []struct {
		name       string
		requestID  string
		wantStored string
	}{
		{name: "valid", requestID: "orchestrator-123", wantStored: "orchestrator-123"},
		{name: "trims surrounding spaces", requestID: "  orchestrator-123  ", wantStored: "orchestrator-123"},
		{name: "invalid", requestID: strings.Repeat("x", maxRequestIDLength+1)},
		{name: "empty", requestID: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := WithRequestID(context.Background(), test.requestID)

			if got := RequestID(ctx); got != test.wantStored {
				t.Fatalf("RequestID(ctx) = %q, want %q", got, test.wantStored)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
		want      bool
	}{
		{name: "exact maximum length", requestID: strings.Repeat("x", maxRequestIDLength), want: true},
		{name: "one over maximum length", requestID: strings.Repeat("x", maxRequestIDLength+1), want: false},
		{name: "newline", requestID: "request\nid", want: false},
		{name: "tab", requestID: "request\tid", want: false},
		{name: "space", requestID: "request id", want: false},
		{name: "non ASCII", requestID: "identificador-ñ", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsValid(test.requestID); got != test.want {
				t.Fatalf("IsValid(%q) = %t, want %t", test.requestID, got, test.want)
			}
		})
	}
}

func TestNewRequestIDIsUniqueAcrossCalls(t *testing.T) {
	seen := make(map[string]struct{}, 1000)

	for range 1000 {
		requestID := newRequestID()
		if _, exists := seen[requestID]; exists {
			t.Fatalf("generated duplicate request ID %q", requestID)
		}
		seen[requestID] = struct{}{}
	}
}

func isBase32(requestID string) bool {
	for _, character := range requestID {
		if (character < 'A' || character > 'Z') && (character < '2' || character > '7') {
			return false
		}
	}
	return true
}
