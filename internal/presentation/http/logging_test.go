//go:build unit

package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

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
