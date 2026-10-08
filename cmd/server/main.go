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
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
	httppres "github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/presentation/http"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

func buildExtractor(cfg *config.Config) (usecase.Extractor, error) {
	extractor := pdf.NewExtractor(cfg.MaxPages)
	// Boot probe: the engine rejecting an invalid payload proves the native
	// library loaded; any other failure must stop the server before traffic.
	if _, _, err := extractor.Extract(context.Background(), []byte("startup probe")); err != nil &&
		!errors.Is(err, domain.ErrInvalidPDF) {
		return nil, fmt.Errorf("pdf_oxide unavailable: %w", err)
	}
	return extractor, nil
}

func main() {
	cfg := config.Load()

	extractor, err := buildExtractor(cfg)
	if err != nil {
		log.Fatalf("extractor unavailable: %v", err)
	}

	uc := usecase.NewExtractUsecase(extractor)

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
