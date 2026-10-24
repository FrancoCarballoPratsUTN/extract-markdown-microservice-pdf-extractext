package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

// newRecordingLogger returns a logger that writes JSON records to the returned
// buffer.
func newRecordingLogger(buffer *bytes.Buffer) *slog.Logger {
	return logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: buffer})
}

// The access log is the audit trail of the service: it must record the
// outcome of the request without ever echoing what the client sent.
func TestWithRequestLogRecordsTheRequestOutcome(t *testing.T) {
	var output bytes.Buffer
	handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("body"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("payload")))

	record := decodeLogLine(t, output.Bytes())
	if record["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", record["method"])
	}
	if record["path"] != "/extract" {
		t.Errorf("path = %v, want /extract", record["path"])
	}
	if record["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want %d", record["status"], http.StatusCreated)
	}
	if _, ok := record["duration_ms"]; !ok {
		t.Error("the access log has no duration_ms field")
	}
	if strings.Contains(output.String(), "payload") {
		t.Errorf("the access log leaked the request body: %s", output.String())
	}
}

// The level of the access log follows the status: a 5xx is our fault, a 4xx is
// the caller's, and everything else is normal traffic.
func TestAccessLogLevelFollowsStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		level  string
	}{
		{"200 is info", http.StatusOK, "INFO"},
		{"400 is warn", http.StatusBadRequest, "WARN"},
		{"404 is warn", http.StatusNotFound, "WARN"},
		{"500 is error", http.StatusInternalServerError, "ERROR"},
		{"503 is error", http.StatusServiceUnavailable, "ERROR"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))

			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/extract", nil))

			record := decodeLogLine(t, output.Bytes())
			if record["level"] != tc.level {
				t.Errorf("level = %v, want %s", record["level"], tc.level)
			}
		})
	}
}

// A panic is a defect, not a reason to lose the process: it becomes a 500 and
// leaves a record with the stack.
func TestWithRecoveryTurnsAPanicIntoA500(t *testing.T) {
	var output bytes.Buffer
	handler := withRecovery(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	record := decodeLogLine(t, output.Bytes())
	if record["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", record["level"])
	}
	if record["panic"] != "boom" {
		t.Errorf("panic = %v, want %q", record["panic"], "boom")
	}
	if _, ok := record["stack"]; !ok {
		t.Error("the panic record has no stack")
	}
}

// A panic is also a request: it has to appear in the access log with its 500,
// or the audit trail of the service would be missing exactly the failures.
func TestAccessLogRecordsAPanickingRequest(t *testing.T) {
	var output bytes.Buffer
	logger := newRecordingLogger(&output)
	router := Routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	records := decodeLogLines(t, output.Bytes())
	access := findLogRecord(t, records, "http request")
	if access["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("access log status = %v, want %d", access["status"], http.StatusInternalServerError)
	}
}

// A response that already started cannot be rewritten: a second document would
// leave the client with a corrupted body and a status that lies.
func TestRecoveryDoesNotCorruptAResponseThatAlreadyStarted(t *testing.T) {
	var output bytes.Buffer
	logger := newRecordingLogger(&output)
	handler := withRequestLog(logger)(withRecovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":"partial`))
		panic("boom")
	})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("the body was corrupted by a second document: %s", rec.Body.String())
	}
}

// The panic value and the stack come from the process, not from a client, but
// they still have to be bounded: a stack is kilobytes and a panic value can
// carry the data that caused it.
func TestThePanicRecordBoundsTheValueAndTheStack(t *testing.T) {
	var output bytes.Buffer
	handler := withRecovery(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(strings.Repeat("x", logging.MaxValueLength*2))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/extract", nil))

	record := decodeLogLine(t, output.Bytes())
	value, _ := record["panic"].(string)
	if got := len([]rune(value)); got != logging.MaxValueLength+1 {
		t.Errorf("the panic value has %d runes, want it truncated to %d", got, logging.MaxValueLength+1)
	}
	stack, _ := record["stack"].(string)
	if len(stack) > logging.MaxStackLength+1 {
		t.Errorf("the stack has %d bytes, want it bounded to %d", len(stack), logging.MaxStackLength)
	}
}

// The health probe is consulted several times per second by the orchestrator:
// logging it would bury the lines that matter.
func TestAccessLogSkipsTheHealthProbe(t *testing.T) {
	var output bytes.Buffer
	handler := withRequestLog(newRecordingLogger(&output))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if output.Len() != 0 {
		t.Errorf("the health probe produced a log line: %s", output.String())
	}
}

// The whole point of the log is to be useful without being dangerous: the
// content of the PDF must never reach stdout.
//
// The test drives the real pipeline (pool, queued extractor, handler and
// router) with an extractor that returns a recognisable string: a stub handler
// would never put the document on the path that could leak it.
func TestThePipelineNeverLogsTheDocumentContent(t *testing.T) {
	const secret = "SECRET-PDF-CONTENT"

	var output bytes.Buffer
	logger := newRecordingLogger(&output)

	pool := usecase.NewWorkerPool(
		usecase.NewExtractUsecase(secretExtractor{content: secret}),
		1, 1, 1<<20, logger,
	)
	t.Cleanup(pool.Close)

	router := Routes(NewHandler(usecase.NewQueuedExtractor(pool, logger), 1, logger), logger)

	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 "+secret))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	// The extraction really happened: without this assertion the test would
	// pass even if the pipeline had been short-circuited.
	if !strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("the response has no extracted content: %s", rec.Body.String())
	}
	if strings.Contains(output.String(), secret) {
		t.Errorf("the log leaked the document content: %s", output.String())
	}
}

// secretExtractor returns a fixed content, whatever the document is.
type secretExtractor struct {
	content string
}

func (e secretExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	return e.content, 1, nil
}
