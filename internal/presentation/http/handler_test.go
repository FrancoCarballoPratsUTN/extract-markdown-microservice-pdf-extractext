//go:build unit

package http

import (
	"bytes"
	"context"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
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
		logger:      logging.Discard(),
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
		logger:      logging.Discard(),
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

func TestExtractHandlerTooManyPagesReturns413RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")

	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{err: domain.ErrTooLarge})
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

func TestExtractHandlerReturnsHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h := newTestHandler(&fakeUsecase{})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("expected ok, got %q", body)
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

func rawPDFRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "application/pdf")
	return req
}

func TestExtractHandlerLogsEachRejection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     int
		level      string
		usecaseErr error
		maxBytes   func(int) int64
		newReq     func() *http.Request
	}{
		{
			name:   "method not allowed",
			status: http.StatusMethodNotAllowed,
			level:  "WARN",
			newReq: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/extract", nil)
			},
		},
		{
			name:   "missing file",
			status: http.StatusBadRequest,
			level:  "WARN",
			newReq: func() *http.Request {
				body := &bytes.Buffer{}
				w := multipart.NewWriter(body)
				_ = w.WriteField("foo", "bar")
				_ = w.Close()
				req := httptest.NewRequest(http.MethodPost, "/extract", body)
				req.Header.Set("Content-Type", w.FormDataContentType())
				return req
			},
		},
		{
			name:   "unsupported media type",
			status: http.StatusBadRequest,
			level:  "WARN",
			newReq: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("<html/>"))
				req.Header.Set("Content-Type", "text/html")
				return req
			},
		},
		{
			name:     "payload too large",
			status:   http.StatusRequestEntityTooLarge,
			level:    "WARN",
			maxBytes: func(int) int64 { return 4 },
			newReq: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 big"))
				req.Header.Set("Content-Type", "application/pdf")
				return req
			},
		},
		{
			name:       "malformed pdf",
			status:     http.StatusBadRequest,
			level:      "WARN",
			usecaseErr: domain.ErrInvalidPDF,
			newReq:     rawPDFRequest,
		},
		{
			name:       "exceeds page limit",
			status:     http.StatusRequestEntityTooLarge,
			level:      "WARN",
			usecaseErr: domain.ErrTooLarge,
			newReq:     rawPDFRequest,
		},
		{
			name:       "extraction failed",
			status:     http.StatusInternalServerError,
			level:      "ERROR",
			usecaseErr: domain.ErrExtractionFailed,
			newReq:     rawPDFRequest,
		},
		{
			name:       "extraction unavailable",
			status:     http.StatusServiceUnavailable,
			level:      "ERROR",
			usecaseErr: domain.ErrServiceUnavailable,
			newReq:     rawPDFRequest,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

			h := &handler{
				uc:          &fakeUsecase{err: tc.usecaseErr},
				maxUploadMB: 50,
				logger:      logger,
				maxBytes: func(mb int) int64 {
					if tc.maxBytes != nil {
						return tc.maxBytes(mb)
					}
					return int64(mb) << 20
				},
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.newReq())

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}

			record := decodeLogLine(t, output.Bytes())
			if got := record["status"]; got != float64(tc.status) {
				t.Errorf("log status = %v, want %d", got, tc.status)
			}
			if got := record["level"]; got != tc.level {
				t.Errorf("log level = %v, want %s", got, tc.level)
			}
			if strings.Contains(output.String(), "%PDF") {
				t.Errorf("log leaked request body bytes: %s", output.String())
			}
		})
	}
}
