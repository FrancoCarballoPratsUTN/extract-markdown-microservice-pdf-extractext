package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/config"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
	httppres "github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/presentation/http"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

func main() {
	cfg := config.Load()

	extractor, closeEngine, err := buildExtractor(cfg)
	if err != nil {
		log.Fatalf("extractor unavailable: %v", err)
	}
	defer closeEngine()

	uc := usecase.NewExtractUsecase(extractor)

	// The queue absorbs bursts instead of rejecting them: workers size the CPU
	// parallelism, and the queue holds what is waiting. Admitting one request
	// per running job plus one per queue slot means the middleware rejects
	// exactly when the pool itself has no room.
	writeTimeout := time.Duration(cfg.WriteTimeout) * time.Second
	jobs := usecase.NewWorkerPool(uc, cfg.MaxWorkers, cfg.QueueSize, int64(cfg.QueueMaxMB)<<20)
	defer jobs.Close()

	h := httppres.NewHandler(usecase.NewQueuedExtractor(jobs), cfg.MaxUploadMB)
	mw := httppres.NewAdmissionMiddleware(cfg.MaxWorkers + cfg.QueueSize)
	handler := mw.Middleware(httppres.WithDeadline(writeTimeout)(h))

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: writeTimeout,
	}

	go func() {
		log.Printf("starting server on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

// buildExtractor resolves EXTRACTOR_ENGINE to an adapter, plus the cleanup that
// releases whatever that engine needs. Selecting the engine by name lets the
// benchmark compare them over HTTP without touching the business layer.
func buildExtractor(cfg *config.Config) (usecase.Extractor, func(), error) {
	switch cfg.Engine {
	case "mupdf":
		log.Printf("extractor: mupdf (in-process)")
		return mupdf.NewExtractor(), func() {}, nil
	case "pdfium", "":
		pool, err := pdf.NewPool(pdf.PoolConfig{
			WorkerPath: cfg.PdfiumWorkerPath,
			Workers:    cfg.MaxWorkers,
			OnLogEvent: func(event string) { log.Printf("pdfium pool: %s", event) },
		})
		if err != nil {
			return nil, nil, fmt.Errorf("pdfium pool: %w", err)
		}
		return pdf.NewExtractor(pool), func() { _ = pool.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown EXTRACTOR_ENGINE %q, want pdfium or mupdf", cfg.Engine)
	}
}
