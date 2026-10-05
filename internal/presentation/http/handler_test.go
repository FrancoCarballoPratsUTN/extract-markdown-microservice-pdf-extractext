//go:build unit

package http

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
)

type fakeUsecase struct {
	resp domain.ExtractResponse
	err  error
}

func (f *fakeUsecase) Extract(ctx context.Context, data []byte) (domain.ExtractResponse, error) {
	return f.resp, f.err
}

func newTestHandler(uc ExtractUsecase) *handler {
	return &handler{
		uc: uc,
		maxBytes: func(mb int) int64 {
			return int64(mb) << 20
		},
		maxUploadMB: 50,
	}
}

func TestExtractHandlerSuccessMultipart(t *testing.T) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	p, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("%PDF-1.7")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{resp: domain.ExtractResponse{Content: "hello", PageCount: 2}})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
	if !strings.Contains(rec.Body.String(), `"content":"hello"`) || !strings.Contains(rec.Body.String(), `"page_count":2`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestExtractHandlerSuccessRawPDF(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{resp: domain.ExtractResponse{Content: "ok", PageCount: 1}})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestExtractHandlerMissingFileReturns400RFC9457(t *testing.T) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	if err := w.WriteField("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":400`) {
		t.Fatalf("expected problem details, got %s", rec.Body.String())
	}
}

func TestExtractHandlerUnsupportedMediaTypeReturns400RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("<html/>"))
	req.Header.Set("Content-Type", "text/html")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
}

func TestExtractHandlerPayloadTooLargeReturns413RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 big"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := &handler{
		uc:          &fakeUsecase{},
		maxBytes:    func(mb int) int64 { return 4 },
		maxUploadMB: 1,
	}
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":413`) {
		t.Fatalf("expected problem details 413, got %s", rec.Body.String())
	}
}

func TestExtractHandlerInvalidPDFReturns400RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{err: domain.ErrInvalidPDF})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
}

func TestExtractHandlerExtractionFailedReturns500RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{err: domain.ErrExtractionFailed})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
}

func TestExtractHandlerServiceUnavailableReturns503RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{err: domain.ErrServiceUnavailable})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected application/problem+json, got %s", ct)
	}
}
