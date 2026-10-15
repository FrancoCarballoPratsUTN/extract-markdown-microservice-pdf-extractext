package reqctx

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerWithRequestID(t *testing.T) {
	t.Parallel()

	var output buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	ctx := WithRequestID(context.Background(), "req-123")
	got := Logger(ctx, logger)

	if got == nil {
		t.Fatal("Logger returned nil")
	}

	got.Info("hello")

	if output.String() == "" {
		t.Fatal("expected log output to be written")
	}
	if !contains(output.String(), "request_id") {
		t.Errorf("expected request_id in output, got: %s", output.String())
	}
	if !contains(output.String(), "req-123") {
		t.Errorf("expected req-123 in output, got: %s", output.String())
	}
}

func TestLoggerWithoutRequestIDReturnsOriginal(t *testing.T) {
	t.Parallel()

	var output buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	got := Logger(context.Background(), logger)
	if got != logger {
		t.Fatal("expected the original logger instance when no request_id is present")
	}
	got.Info("ok")
	if output.String() == "" {
		t.Fatal("expected log output when no request_id is present")
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Parallel()

	ctx := WithRequestID(context.Background(), "abc-def")
	if id := RequestID(ctx); id != "abc-def" {
		t.Errorf("RequestID = %q, want %q", id, "abc-def")
	}
	if id := RequestID(context.Background()); id != "" {
		t.Errorf("RequestID of empty ctx = %q, want empty", id)
	}
}

type buffer struct {
	data []byte
}

func (b *buffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *buffer) String() string {
	return string(b.data)
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(s, sub)
}
