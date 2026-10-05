package http

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

type AdmissionMiddleware struct {
	sem     chan struct{}
	once    sync.Once
	maxConc int
}

func NewAdmissionMiddleware(maxConcurrent int) *AdmissionMiddleware {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &AdmissionMiddleware{
		sem:     make(chan struct{}, maxConcurrent),
		maxConc: maxConcurrent,
	}
}

func (m *AdmissionMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
			next.ServeHTTP(w, r)
		default:
			domain.WriteProblem(w, domain.NewProblem(http.StatusServiceUnavailable, "Service Unavailable", "server is saturated"))
		}
	})
}

// WithDeadline gives every request a context deadline of timeout. A queued
// extraction is bounded by that deadline instead of running until the client
// disconnects, which keeps the queue draining predictably under saturation.
func WithDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
