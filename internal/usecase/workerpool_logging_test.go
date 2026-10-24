package usecase

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

func TestWorkerPoolLogsCompletedExtraction(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{content: "hola", pageCount: 3}), 1, 1, 1<<20, logger)
	defer wp.Close()

	ch, err := wp.Submit(context.Background(), []byte("pdf"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-ch

	if !strings.Contains(output.String(), "extraction completed") {
		t.Errorf("the extraction log has no completion line: %s", output.String())
	}
}

// A rejected job is the symptom of a saturated replica: it has to be visible
// in the log without being an error of the caller.
func TestWorkerPoolLogsAWarningWhenTheQueueIsFull(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 1, 1<<20, logger)
	defer func() {
		close(gate)
		wp.Close()
	}()

	ctx := context.Background()
	if _, err := wp.Submit(ctx, []byte("a")); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// Let the worker pick the first job up so the second one lands in the
	// queue buffer and the third one finds it full.
	time.Sleep(10 * time.Millisecond)
	if _, err := wp.Submit(ctx, []byte("b")); err != nil {
		t.Fatalf("second submit: %v", err)
	}
	if _, err := wp.Submit(ctx, []byte("c")); err == nil {
		t.Fatal("the third submit was accepted, want the queue to be full")
	}

	if !strings.Contains(output.String(), "queue full") {
		t.Errorf("the rejection was not logged: %s", output.String())
	}
	if strings.Contains(output.String(), "ERROR") {
		t.Errorf("a rejected job was logged as an error: %s", output.String())
	}
}
