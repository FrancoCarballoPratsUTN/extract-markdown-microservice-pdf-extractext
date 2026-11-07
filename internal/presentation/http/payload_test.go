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

// multipartBodyWithPDF construye un body multipart cuya parte "file" contiene
// pdfSize bytes. Un trailingSize mayor que cero añade un campo que no es fichero
// después del documento, que es lo que ejercita el tope del body y no el de la
// parte.
func multipartBodyWithPDF(t *testing.T, pdfSize, trailingSize int) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(pdfFormField, "doc.pdf")
	if err != nil {
		t.Fatalf("failed to build multipart body: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("P"), pdfSize)); err != nil {
		t.Fatalf("failed to write PDF part: %v", err)
	}
	if trailingSize > 0 {
		if err := writer.WriteField("metadata", strings.Repeat("x", trailingSize)); err != nil {
			t.Fatalf("failed to write metadata: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart body: %v", err)
	}
	return body, writer.FormDataContentType()
}

// Un PDF de exactamente el límite configurado debe aceptarse con cualquier
// Content-Type que lo transporte: el envoltorio multipart no forma parte del
// documento.
func TestReadPDFPayloadAcceptsMultipartPDFExactlyAtTheLimit(t *testing.T) {
	const limit = 4096
	body, contentType := multipartBodyWithPDF(t, limit, 0)

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", contentType)

	data, err := readPDFPayload(req, limit)
	if err != nil {
		t.Fatalf("a PDF of exactly the limit must be accepted: %v", err)
	}
	if len(data) != limit {
		t.Fatalf("len(data) = %d, want %d", len(data), limit)
	}
}

// El tope del documento sigue aplicándose a la propia parte del fichero.
func TestReadPDFPayloadRejectsMultipartPDFAboveTheLimit(t *testing.T) {
	const limit = 4096
	body, contentType := multipartBodyWithPDF(t, limit+1, 0)

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", contentType)

	_, err := readPDFPayload(req, limit)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

// El body tiene su propio tope, mayor: el margen del envoltorio no debe
// convertirse en una lectura sin límite de una parte multipart que no es el
// documento.
func TestReadPDFPayloadRejectsMultipartBodyAboveTheLimit(t *testing.T) {
	const limit = 1024
	body, contentType := multipartBodyWithPDF(t, 8, limit+multipartOverheadAllowance+1)

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", contentType)

	_, err := readPDFPayload(req, limit)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

// Un campo enorme que no es fichero, colocado antes del documento, debe ser un
// 413 mientras el lector multipart sigue parseando partes, no un 400 "missing
// pdf file".
func TestReadPDFPayloadRejectsMultipartBodyAboveTheLimitBeforeTheFile(t *testing.T) {
	const limit = 1024
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("metadata", strings.Repeat("x", limit+multipartOverheadAllowance+1)); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	part, err := writer.CreateFormFile(pdfFormField, "doc.pdf")
	if err != nil {
		t.Fatalf("failed to build multipart body: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.7")); err != nil {
		t.Fatalf("failed to write PDF part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = readPDFPayload(req, limit)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

// brokenAfterReader sirve todo su payload una vez y luego falla, simulando una
// conexión que se corta después de recibir la parte del fichero.
type brokenAfterReader struct {
	data []byte
}

func (b *brokenAfterReader) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, errors.New("connection reset by peer")
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

// Un body que se rompe después de la parte del fichero es un fallo de
// transporte, no un fichero ausente ni un payload demasiado grande.
func TestReadPDFPayloadReportsATruncatedMultipartBody(t *testing.T) {
	body, contentType := multipartBodyWithPDF(t, 16, 0)
	req := httptest.NewRequest(http.MethodPost, "/extract", &brokenAfterReader{data: body.Bytes()})
	req.Header.Set("Content-Type", contentType)

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrUnreadableBody) {
		t.Fatalf("expected ErrUnreadableBody, got %v", err)
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

func TestReadPDFPayloadRejectsMalformedContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("%PDF-1.7"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Errorf("expected ErrUnsupportedMediaType, got %v", err)
	}
}

func TestReadPDFPayloadRejectsMalformedMultipartPartHeader(t *testing.T) {
	body := "--broken\r\n" +
		"This is not a header\r\n" +
		"\r\n" +
		"data\r\n" +
		"--broken--\r\n"
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=broken")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}

func TestReadPDFPayloadRejectsMultipartWithoutBoundary(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "multipart/form-data")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrMissingFile) {
		t.Errorf("expected ErrMissingFile, got %v", err)
	}
}

// failingReader rompe el transporte a mitad del body.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("connection reset by peer")
}

// Un body que no se puede leer hasta el final es un fallo de transporte, no una
// petición sin fichero: los dos merecen respuestas distintas.
func TestReadPDFPayloadReportsAnUnreadableBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/extract", failingReader{})
	req.Header.Set("Content-Type", "application/pdf")

	_, err := readPDFPayload(req, 1<<20)
	if !errors.Is(err, ErrUnreadableBody) {
		t.Errorf("expected ErrUnreadableBody, got %v", err)
	}
	if errors.Is(err, ErrMissingFile) {
		t.Errorf("err = %v, want an unreadable body not to look like a missing file", err)
	}
}
