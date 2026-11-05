package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

func TestWithRequestIDAddsTheField(t *testing.T) {
	var output bytes.Buffer
	logger := NewWithOptions(Options{Level: slog.LevelDebug, Output: &output})
	ctx := reqctx.WithRequestID(context.Background(), "orchestrator-123")

	WithRequestID(ctx, logger).Info("request completed")

	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Fatalf("log = %q, want request_id field", output.String())
	}
}

func TestWithRequestIDWithoutIDLeavesTheLogUnchanged(t *testing.T) {
	var output bytes.Buffer
	logger := NewWithOptions(Options{Level: slog.LevelDebug, Output: &output})

	WithRequestID(context.Background(), logger).Info("request completed")

	if strings.Contains(output.String(), `"request_id"`) {
		t.Fatalf("log = %q, want no request_id field", output.String())
	}
}
