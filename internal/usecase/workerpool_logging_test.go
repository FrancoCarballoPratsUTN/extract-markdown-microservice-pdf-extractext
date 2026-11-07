package usecase

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
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

func TestWorkerPoolPropagatesRequestIDToExtractionLog(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{content: "hola", pageCount: 1}), 1, 1, 1<<20, logger)
	defer wp.Close()

	ctx := reqctx.WithRequestID(context.Background(), "orchestrator-123")
	ch, err := wp.Submit(ctx, []byte("pdf"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-ch

	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Errorf("request ID missing from extraction log: %s", output.String())
	}
}

// Un trabajo rechazado es el síntoma de una réplica saturada: debe ser visible
// en el log sin ser un error del llamador.
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

	// Dejar que el worker tome el primer trabajo para que el segundo caiga en el
	// buffer de la cola y el tercero la encuentre llena.
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

func TestWorkerPoolLogsTheRequestIDWhenTheQueueIsFull(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 1, 1<<20, logger)
	defer func() {
		close(gate)
		wp.Close()
	}()

	ctx := reqctx.WithRequestID(context.Background(), "orchestrator-123")
	if _, err := wp.Submit(ctx, []byte("a")); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := wp.Submit(ctx, []byte("b")); err != nil {
		t.Fatalf("second submit: %v", err)
	}
	if _, err := wp.Submit(ctx, []byte("c")); err == nil {
		t.Fatal("the third submit was accepted, want the queue to be full")
	}

	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Fatalf("queue rejection log missing request ID: %s", output.String())
	}
}
