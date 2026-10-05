package http

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadPDFPayloadFromMultipartFileField(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "doc.pdf")
	if err != nil {
		t.Fatalf("failed to build multipart body: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.7 fake")); err != nil {
		t.Fatalf("failed to write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	data, err := readPDFPayload(req, 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "%PDF-1.7 fake" {
		t.Errorf("unexpected payload: %q", data)
	}
}

func TestReadPDFPayloadFromMultipartMissingFileFieldReturnsErrMissingFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("documento", "doc.pdf")
	if err != nil {
		t.Fatalf("failed to build multipart body: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.7")); err != nil {
		t.Fatalf("failed to write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}

func TestReadPDFPayloadFromMultipartWithoutAnyFilePartReturnsErrMissingFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("nombre", "doc"); err != nil {
		t.Fatalf("failed to write field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}

func TestReadPDFPayloadFromRawBinaryBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 raw"))
	req.Header.Set("Content-Type", "application/pdf")

	data, err := readPDFPayload(req, 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "%PDF-1.7 raw" {
		t.Errorf("unexpected payload: %q", data)
	}
}

func TestReadPDFPayloadFromOctetStreamBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 raw"))
	req.Header.Set("Content-Type", "application/octet-stream")

	if _, err := readPDFPayload(req, 1<<20); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadPDFPayloadRejectsPayloadExceedingMaxBytes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7 too big"))
	req.Header.Set("Content-Type", "application/pdf")

	_, err := readPDFPayload(req, 4)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestReadPDFPayloadRejectsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/pdf")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}

func TestReadPDFPayloadRejectsUnsupportedContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("<html></html>"))
	req.Header.Set("Content-Type", "text/html")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Errorf("expected ErrUnsupportedMediaType, got %v", err)
	}
}

func TestReadPDFPayloadRejectsMalformedMultipartBoundary(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=broken")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}
