package usecase

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/reqctx"
)

// ErrQueueFull indica que la cola acotada no tenía sitio para el trabajo. Los
// llamadores deben responderlo como 503 para que el cliente reintente con
// backoff.
var ErrQueueFull = errors.New("queue full")

// ErrClosed indica que el pool ya no acepta trabajo porque se está apagando.
var ErrClosed = errors.New("worker pool closed")

// JobResult es el resultado de un trabajo: la respuesta extraída o el error.
type JobResult struct {
	Resp domain.ExtractResponse
	Err  error
}

// job es dueño del canal en el que escribe su resultado. Un trabajo pertenece a
// un único submitter, lo que evita que llamadores concurrentes reciban
// documentos ajenos.
type job struct {
	data      []byte
	result    chan JobResult
	requestID string
}

// WorkerPool desacopla el manejo de peticiones HTTP de la extracción, que usa
// CPU. Acota las extracciones en curso, los trabajos en espera y los bytes que
// retienen esos trabajos, de modo que una ráfaga cueste latencia en lugar de
// peticiones rechazadas o un OOM kill.
type WorkerPool struct {
	extractor ExtractUsecase
	jobs      chan job
	wg        sync.WaitGroup
	logger    *slog.Logger

	// budget es el tope de bytes compartido por los trabajos en curso y los
	// encolados: ambos retienen el PDF completo en memoria hasta que la
	// extracción termina.
	budget *byteBudget

	mu     sync.Mutex
	closed bool
}

// byteBudget es un semáforo contador de bytes. Un int64 protegido por un mutex
// es suficiente: Submit se ejecuta una vez por petición, así que la contención
// es irrelevante frente al coste de extracción que protege.
type byteBudget struct {
	mu    sync.Mutex
	limit int64
	used  int64
}

// tryAcquire reserva size bytes y devuelve false cuando el presupuesto está
// agotado.
func (b *byteBudget) tryAcquire(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+size > b.limit {
		return false
	}
	b.used += size
	return true
}

// release devuelve size bytes al presupuesto.
func (b *byteBudget) release(size int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= size
}

// NewWorkerPool crea el pool y arranca sus workers. Un queueSize menor que el
// número de workers se eleva a ese mínimo para que Submit nunca bloquee; un
// maxQueuedBytes no positivo desactiva el tope de bytes.
func NewWorkerPool(extractor ExtractUsecase, workers, queueSize int, maxQueuedBytes int64, logger *slog.Logger) *WorkerPool {
	if workers <= 0 {
		workers = 1
	}
	// La cola existe para que Submit pueda entregar el trabajo sin bloquearse.
	// Eso exige sitio para un trabajo por worker: con un buffer menor, un envío
	// no bloqueante fallaría aunque hubiera un worker libre, rechazando trabajo
	// que el pool podría haber empezado de inmediato.
	if queueSize < workers {
		queueSize = workers
	}
	if maxQueuedBytes <= 0 {
		maxQueuedBytes = math.MaxInt64
	}
	if logger == nil {
		logger = slog.Default()
	}
	wp := &WorkerPool{
		extractor: extractor,
		jobs:      make(chan job, queueSize),
		budget:    &byteBudget{limit: maxQueuedBytes},
		logger:    logger,
	}
	for i := 0; i < workers; i++ {
		wp.wg.Add(1)
		go wp.work()
	}
	return wp
}

func (wp *WorkerPool) work() {
	defer wp.wg.Done()
	for j := range wp.jobs {
		j.result <- wp.run(j)
	}
}

// run extrae un trabajo e informa el resultado.
//
// La extracción corre con su propio contexto porque la conversión nativa no se
// puede cancelar a mitad de llamada: un cliente que se marcha no debe abortar
// el trabajo ya en curso.
func (wp *WorkerPool) run(j job) JobResult {
	start := time.Now()
	extractionContext := reqctx.WithRequestID(context.Background(), j.requestID)
	logger := logging.WithRequestID(extractionContext, wp.logger)
	resp, err := wp.extractor.Extract(extractionContext, j.data)
	duration := time.Since(start).Milliseconds()

	// El PDF ya no es alcanzable desde aquí, así que devuelve su presupuesto
	// antes de entregar el resultado: eso permite que la siguiente ráfaga entre
	// de inmediato.
	wp.budget.release(int64(len(j.data)))

	if err != nil {
		logger.Error("extraction failed", "duration_ms", duration, "error", err)
		return JobResult{Err: err}
	}
	logger.Info("extraction completed", "pages", resp.PageCount, "duration_ms", duration)
	return JobResult{Resp: resp}
}

// Submit encola data y devuelve el canal que transporta su resultado. El canal
// está bufferizado para que un worker nunca se bloquee al entregar, incluso si
// el llamador dejó de esperar.
//
// El trabajo corre con su propio contexto para que un cliente desconectado no
// cancele el trabajo en curso.
func (wp *WorkerPool) Submit(ctx context.Context, data []byte) (chan JobResult, error) {
	size := int64(len(data))

	wp.mu.Lock()
	if wp.closed {
		wp.mu.Unlock()
		return nil, ErrClosed
	}
	if !wp.budget.tryAcquire(size) {
		wp.mu.Unlock()
		wp.logRejection(ctx, size)
		return nil, ErrQueueFull
	}

	j := job{
		data:      data,
		result:    make(chan JobResult, 1),
		requestID: reqctx.RequestID(ctx),
	}
	select {
	case wp.jobs <- j:
		wp.mu.Unlock()
		return j.result, nil
	default:
		wp.budget.release(size)
		wp.mu.Unlock()
		wp.logRejection(ctx, size)
		return nil, ErrQueueFull
	}
}

// logRejection informa que un trabajo no pudo admitirse. Ambos motivos
// significan lo mismo para el llamador: esta réplica está saturada.
func (wp *WorkerPool) logRejection(ctx context.Context, size int64) {
	logging.WithRequestID(ctx, wp.logger).Warn("queue full; rejecting job", "bytes", size)
}

// Wait espera el resultado de un trabajo enviado, o devuelve el error del
// contexto si el deadline vence antes. El write timeout HTTP es el que aporta
// ese deadline, así que un cliente que se marcha deja el trabajo en cola en
// lugar de retener un handler.
func (wp *WorkerPool) Wait(ctx context.Context, ch chan JobResult) (domain.ExtractResponse, error) {
	select {
	case res := <-ch:
		return res.Resp, res.Err
	case <-ctx.Done():
		return domain.ExtractResponse{}, ctx.Err()
	}
}

// Close deja de aceptar trabajo y espera a que terminen los trabajos ya
// encolados.
func (wp *WorkerPool) Close() {
	_ = wp.CloseContext(context.Background())
}

// CloseContext deja de aceptar trabajo y espera a que terminen los trabajos
// encolados, rindiéndose cuando el contexto vence. Un drenaje acotado evita que
// una llamada nativa atascada bloquee el apagado para siempre.
//
// Cuando el contexto vence, la goroutine del worker sigue ejecutándose hasta que
// la llamada nativa retorna: el trabajo aún retiene su PDF en memoria. Eso es
// deliberado —la alternativa es matar el proceso a mitad de conversión— e
// inofensivo aquí porque el llamador está apagando el proceso de todos modos.
func (wp *WorkerPool) CloseContext(ctx context.Context) error {
	wp.mu.Lock()
	if !wp.closed {
		wp.closed = true
		close(wp.jobs)
	}
	wp.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
