package usecase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

func TestQueuedExtractorReturnsTheExtractedDocument(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{content: "hola", pageCount: 3}), 2, 4, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)

	resp, err := NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("pdf"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if resp.Content != "hola" || resp.PageCount != 3 {
		t.Fatalf("resp = %+v, want content %q over %d pages", resp, "hola", 3)
	}
}

// signallingExtractor avisa de que un worker tomó el trabajo antes de
// bloquearse, para que un test pueda llenar la cola sin competir con el pool.
type signallingExtractor struct {
	gate    chan struct{}
	started chan struct{}
}

func (s *signallingExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
	select {
	case <-s.gate:
		return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// Una cola llena es una señal de saturación, no un error del cliente, así que
// debe llegar al cliente como 503 y no como un 500 genérico.
func TestQueuedExtractorReportsAFullQueueAsServiceUnavailable(t *testing.T) {
	gate := make(chan struct{})
	// Bufferizado para que un worker que tome un trabajo posterior no se bloquee
	// en el envío y bloquee Close; el test sólo necesita la primera señal.
	extractor := &signallingExtractor{gate: gate, started: make(chan struct{}, 4)}
	wp := NewWorkerPool(extractor, 1, 1, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)

	// El bloqueante ocupa al worker; esperar la señal es lo que deja libre el
	// sitio de la cola para el relleno de forma determinista.
	blocker, err := wp.Submit(context.Background(), []byte("bloqueante"))
	if err != nil {
		t.Fatalf("submit blocker: %v", err)
	}
	t.Cleanup(func() { drain(t, blocker) })
	// Registrado al final para que el orden LIFO de t.Cleanup abra la compuerta
	// antes del drenaje y de Close, que de otro modo esperarían para siempre al
	// worker bloqueado.
	t.Cleanup(func() { close(gate) })

	select {
	case <-extractor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never picked up the blocking job")
	}

	if _, err := wp.Submit(context.Background(), []byte("ocupa la cola")); err != nil {
		t.Fatalf("submit queue filler: %v", err)
	}

	_, err = NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("rechazado"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
}

// Un pool que se está apagando rechaza el trabajo en lugar de ejecutarlo: el
// cliente debe ver un 503 al que pueda reintentar en otro sitio, no un 500.
func TestQueuedExtractorReportsAClosedPoolAsServiceUnavailable(t *testing.T) {
	wp := NewWorkerPool(NewExtractUsecase(&mockExtractor{}), 1, 1, 1<<20, logging.Discard())
	wp.Close()

	_, err := NewQueuedExtractor(wp, logging.Discard()).Extract(context.Background(), []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
}

// Un trabajo que no termina dentro del deadline de la petición debe salir como
// 503: el cliente debería reintentar en otro sitio, no recibir un 500.
func TestQueuedExtractorReportsAnExpiredDeadlineAsServiceUnavailable(t *testing.T) {
	gate := make(chan struct{})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 4, 1<<20, logging.Discard())
	t.Cleanup(wp.Close)
	t.Cleanup(func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewQueuedExtractor(wp, logging.Discard()).Extract(ctx, []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Extract blocked for %v, want it to give up on the deadline", elapsed)
	}
}

func TestQueuedExtractorLogsTheRequestIDWhenTheRequestIsAbandoned(t *testing.T) {
	var output bytes.Buffer
	gate := make(chan struct{})
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	wp := NewWorkerPool(&gatedExtractor{gate: gate}, 1, 4, 1<<20, logger)
	t.Cleanup(wp.Close)
	t.Cleanup(func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = reqctx.WithRequestID(ctx, "orchestrator-123")

	_, err := NewQueuedExtractor(wp, logger).Extract(ctx, []byte("pdf"))
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
	if !strings.Contains(output.String(), `"request_id":"orchestrator-123"`) {
		t.Fatalf("abandoned extraction log missing request ID: %s", output.String())
	}
}
