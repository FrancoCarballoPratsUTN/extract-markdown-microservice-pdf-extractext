package http

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
)

const (
	pdfFormField = "file"

	// multipartOverheadAllowance covers boundaries, part headers and the
	// closing delimiter. Without it, a PDF of exactly maxBytes would be
	// rejected just because of the multipart envelope.
	multipartOverheadAllowance = 8 << 10
)

var (
	ErrMissingFile          = errors.New("missing pdf file")
	ErrPayloadTooLarge      = errors.New("payload too large")
	ErrUnsupportedMediaType = errors.New("unsupported media type")
	// ErrUnreadableBody means the body could not be read to the end. It is
	// distinct from ErrMissingFile: a truncated upload is a transport
	// problem, not a request without a file.
	ErrUnreadableBody = errors.New("could not read request body")
)

// readPDFPayload extracts the PDF bytes from the request, accepting either a
// multipart/form-data upload on the "file" field or a raw binary body.
func readPDFPayload(r *http.Request, maxBytes int64) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, ErrUnsupportedMediaType
	}

	switch mediaType {
	case "multipart/form-data":
		return readMultipartPDF(r, maxBytes, maxBytes+multipartOverheadAllowance)
	case "application/pdf", "application/octet-stream":
		return readLimited(r.Body, maxBytes)
	default:
		return nil, ErrUnsupportedMediaType
	}
}

// readMultipartPDF extracts the "file" part, bounded by maxPartBytes, and
// requires the whole body to fit in maxBodyBytes. The body cap is larger
// because the multipart envelope (boundaries and part headers) is not part of
// the document.
func readMultipartPDF(r *http.Request, maxPartBytes, maxBodyBytes int64) ([]byte, error) {
	// nil is safe: MaxBytesReader only type-asserts the writer to flag the
	// connection to net/http. The 413 is written by the handler.
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)

	reader, err := r.MultipartReader()
	if err != nil {
		return nil, ErrMissingFile
	}
	data, err := readFilePart(reader, maxPartBytes)
	if err != nil {
		return nil, err
	}
	if err := ensureBodyConsumed(r.Body); err != nil {
		return nil, err
	}
	return data, nil
}

// readFilePart returns the bytes of the first part named "file" that carries a
// filename. Other parts are skipped.
func readFilePart(reader *multipart.Reader, maxPartBytes int64) ([]byte, error) {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, ErrMissingFile
		}
		if err != nil {
			if isPayloadTooLarge(err) {
				return nil, ErrPayloadTooLarge
			}
			return nil, ErrMissingFile
		}

		if part.FormName() != pdfFormField || part.FileName() == "" {
			_ = part.Close()
			continue
		}

		data, err := readLimited(part, maxPartBytes)
		_ = part.Close()
		return data, err
	}
}

// ensureBodyConsumed drains the rest of the request so an oversized body is
// reported as such instead of being silently ignored.
func ensureBodyConsumed(body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		if isPayloadTooLarge(err) {
			return ErrPayloadTooLarge
		}
		return ErrUnreadableBody
	}
	return nil
}

func isPayloadTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

// readLimited reads at most maxBytes, reporting ErrPayloadTooLarge when the
// reader holds more data than allowed.
func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, ErrUnreadableBody
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrPayloadTooLarge
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrMissingFile
	}
	return data, nil
}
