//go:build unit

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// decodeLogLine decodes a single JSON log record from the buffer. It fails the
// test if the buffer is empty or not a valid single JSON object.
func decodeLogLine(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatal("expected a log record, got none")
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log record is not valid JSON: %v (%q)", err, line)
	}
	return record
}

func TestLogDecisionLevelFollowsStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		level  string
	}{
		{"200 is info", http.StatusOK, "INFO"},
		{"201 is info", http.StatusCreated, "INFO"},
		{"400 is warn", http.StatusBadRequest, "WARN"},
		{"404 is warn", http.StatusNotFound, "WARN"},
		{"413 is warn", http.StatusRequestEntityTooLarge, "WARN"},
		{"500 is error", http.StatusInternalServerError, "ERROR"},
		{"503 is error", http.StatusServiceUnavailable, "ERROR"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

			logDecision(context.Background(), logger, "decision", tc.status)

			record := decodeLogLine(t, output.Bytes())
			if got := record["level"]; got != tc.level {
				t.Errorf("level = %v, want %s", got, tc.level)
			}
			if got := record["status"]; got != float64(tc.status) {
				t.Errorf("status = %v, want %d", got, tc.status)
			}
		})
	}
}

func TestLogDecisionIncludesRequestIDWhenPresent(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	ctx := reqctx.WithRequestID(context.Background(), "req-abc")

	logDecision(ctx, logger, "decision", http.StatusBadRequest)

	record := decodeLogLine(t, output.Bytes())
	if got := record["request_id"]; got != "req-abc" {
		t.Errorf("request_id = %v, want %q", got, "req-abc")
	}
}

func TestLogDecisionFallsBackToDefaultLoggerWhenNil(t *testing.T) {
	var output bytes.Buffer
	original := slog.Default()
	slog.SetDefault(logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output}))
	t.Cleanup(func() { slog.SetDefault(original) })

	logDecision(context.Background(), nil, "decision", http.StatusInternalServerError)

	record := decodeLogLine(t, output.Bytes())
	if got := record["level"]; got != "ERROR" {
		t.Errorf("level = %v, want ERROR", got)
	}
}
