package main

import (
	"context"
	"errors"
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

	pool, err := pdf.NewPool(pdf.PoolConfig{
		WorkerPath: cfg.PdfiumWorkerPath,
		Workers:    cfg.MaxWorkers,
		OnLogEvent: func(event string) { log.Printf("pdfium pool: %s", event) },
	})
	if err != nil {
		log.Fatalf("pdfium pool unavailable: %v", err)
	}
	defer pool.Close()

	extractor := pdf.NewExtractor(pool)
	uc := usecase.NewExtractUsecase(extractor)

	h := httppres.NewHandler(uc, cfg.MaxUploadMB)
	mw := httppres.NewAdmissionMiddleware(cfg.MaxWorkers)
	handler := mw.Middleware(h)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
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
