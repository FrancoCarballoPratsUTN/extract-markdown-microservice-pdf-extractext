package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

// Un pool que se está apagando debe decirlo en lugar de aceptar un trabajo que
// nunca ejecutará.
func TestWorkerPoolRejectsSubmitsAfterClose(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{}), 1, 1, 1<<20, logging.Discard())
	wp.Close()

	_, err := wp.Submit(context.Background(), []byte("pdf"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

// El drenaje es acotado: un worker atascado en una llamada nativa no debe
// mantener el apagado esperando para siempre.
func TestWorkerPoolCloseContextGivesUpWhenTheDeadlineExpires(t *testing.T) {
	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 1, 1<<20, logging.Discard())
	t.Cleanup(func() { close(gate) })

	if _, err := wp.Submit(context.Background(), []byte("pdf")); err != nil {
		t.Fatalf("submit: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := wp.CloseContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CloseContext error = %v, want context.DeadlineExceeded", err)
	}
}

// Un pool que drenó limpiamente no reporta error: el apagado puede continuar.
func TestWorkerPoolCloseContextReturnsNilWhenTheJobsFinish(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{}), 1, 1, 1<<20, logging.Discard())

	if _, err := wp.Submit(context.Background(), []byte("pdf")); err != nil {
		t.Fatalf("submit: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := wp.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext error = %v, want no error", err)
	}
}
