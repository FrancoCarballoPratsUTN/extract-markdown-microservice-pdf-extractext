package http

import (
	"net/http"
	"sync"

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
			return
		}
	})
}
