package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// La espera en cola está acotada por un deadline de contexto, y
// http.Server.WriteTimeout no lo proporciona: sin esto el handler esperaría un
// trabajo encolado hasta que el cliente se rindiera.
func TestDeadlineMiddlewareBoundsTheHandlerContext(t *testing.T) {
	var observed time.Duration
	handler := WithDeadline(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("request context has no deadline")
			return
		}
		observed = time.Until(deadline)
		time.Sleep(500 * time.Millisecond)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/extract", nil))

	if observed <= 0 || observed > 50*time.Millisecond {
		t.Fatalf("deadline in %v, want a positive value no larger than 50ms", observed)
	}
}

// El contexto derivado debe cancelarse cuando el handler retorna, o el timer
// del deadline se filtra durante toda la vida del proceso.
func TestDeadlineMiddlewareCancelsWhenTheHandlerReturns(t *testing.T) {
	var captured context.Context
	handler := WithDeadline(time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/extract", nil))

	select {
	case <-captured.Done():
	case <-time.After(time.Second):
		t.Fatal("context still alive after the handler returned")
	}
}
