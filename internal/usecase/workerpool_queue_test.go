package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

// gatedExtractor bloquea cada extracción hasta que se cierra gate, lo que
// permite a un test retener trabajos en el pool y observar el comportamiento de
// admisión de forma determinista.
type gatedExtractor struct {
	gate chan struct{}
}

func (g *gatedExtractor) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	select {
	case <-g.gate:
		return domain.ExtractResponse{Content: string(data), PageCount: 1}, nil
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// newGatedPool devuelve un pool cuyos workers están bloqueados en una compuerta
// sin abrir, una función idempotente que la abre y un cleanup que siempre la
// abre antes de que Close espere a los workers.
func newGatedPool(t *testing.T, workers, queueSize int, maxQueuedBytes int64) (*WorkerPool, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }

	wp := NewWorkerPool(&gatedExtractor{gate: gate}, workers, queueSize, maxQueuedBytes, logging.Discard())
	t.Cleanup(wp.Close)
	t.Cleanup(open)
	return wp, open
}

func drain(t *testing.T, ch chan JobResult) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("job never completed")
	}
}

// Una ráfaga mayor que el número de workers debe esperar en la cola en lugar de
// ser rechazada: esa es toda la razón de ser de la cola.
func TestWorkerPoolAbsorbsBurstBeyondWorkerCount(t *testing.T) {
	wp, open := newGatedPool(t, 1, 8, 1<<20)

	channels := make([]chan JobResult, 0, 4)
	for i := range 4 {
		ch, err := wp.Submit(context.Background(), []byte("hola"))
		if err != nil {
			t.Fatalf("submit %d rejected with %v, want the queue to absorb it", i, err)
		}
		channels = append(channels, ch)
	}

	open()
	for _, ch := range channels {
		drain(t, ch)
	}
}

// El presupuesto de bytes es lo que mantiene una cola de PDFs grandes dentro del
// límite de memoria del contenedor, así que debe rechazar antes de que el
// proceso muera por OOM.
func TestWorkerPoolRejectsWhenByteBudgetExhausted(t *testing.T) {
	wp, _ := newGatedPool(t, 1, 8, 16) // sitio para exactamente dos payloads de 8 bytes

	if _, err := wp.Submit(context.Background(), make([]byte, 8)); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := wp.Submit(context.Background(), make([]byte, 8)); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	_, err := wp.Submit(context.Background(), make([]byte, 8))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull once the byte budget is spent", err)
	}
}

// Los bytes pertenecen a la vida de un trabajo, no a la de la cola: un trabajo
// que terminó la extracción debe devolver su presupuesto o el servicio se
// atasca con el tiempo.
func TestWorkerPoolReleasesBytesAfterExtraction(t *testing.T) {
	wp, open := newGatedPool(t, 1, 8, 16)

	ch, err := wp.Submit(context.Background(), make([]byte, 8))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	open()
	drain(t, ch)

	if _, err := wp.Submit(context.Background(), make([]byte, 8)); err != nil {
		t.Fatalf("submit after completion: %v, want the freed budget to be reusable", err)
	}
}

// Un cliente que se rinde no debe dejar su handler bloqueado para siempre
// esperando un resultado que nadie va a leer.
func TestWorkerPoolExtractTimesOutInsteadOfHanging(t *testing.T) {
	wp, _ := newGatedPool(t, 1, 8, 1<<20)

	ch, err := wp.Submit(context.Background(), []byte("hola"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := wp.Wait(ctx, ch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait blocked for %v, want it to give up on the deadline", elapsed)
	}
}
