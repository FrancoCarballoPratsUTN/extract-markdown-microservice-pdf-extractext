//go:build unit

package http

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAdmissionMiddlewareAllowsUpToMaxConcurrent(t *testing.T) {
	mw := NewAdmissionMiddleware(2)
	var mu sync.Mutex
	count := 0
	blocked := make(chan struct{})

	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		<-blocked
		w.WriteHeader(http.StatusOK)
	}))

	go func() {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	go func() {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	c := count
	mu.Unlock()
	if c != 2 {
		t.Fatalf("expected 2 concurrent, got %d", c)
	}

	close(blocked)
	time.Sleep(10 * time.Millisecond)
}

func TestAdmissionMiddlewareRejectsWhenSaturated(t *testing.T) {
	mw := NewAdmissionMiddleware(1)
	blocked := make(chan struct{})
	rejected := make(chan int, 1)

	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
		w.WriteHeader(http.StatusOK)
	}))

	go func() {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	time.Sleep(10 * time.Millisecond)

	go func() {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))
		rejected <- resp.Code
	}()

	select {
	case code := <-rejected:
		if code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", code)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for rejection")
	}

	close(blocked)
}

func TestAdmissionMiddlewareReturnsRFC9457Problem(t *testing.T) {
	mw := NewAdmissionMiddleware(1)
	blocked := make(chan struct{})

	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
		w.WriteHeader(http.StatusOK)
	}))

	go func() {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	time.Sleep(10 * time.Millisecond)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Code)
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}

	close(blocked)
}
