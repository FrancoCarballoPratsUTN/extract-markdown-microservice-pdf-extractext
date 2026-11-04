package http

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
)

const pdfFormField = "file"

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
		return readMultipartPDF(r, maxBytes)
	case "application/pdf", "application/octet-stream":
		return readLimited(r.Body, maxBytes)
	default:
		return nil, ErrUnsupportedMediaType
	}
}

func readMultipartPDF(r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, ErrMissingFile
	}

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

		data, err := readLimited(part, maxBytes)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			if isPayloadTooLarge(err) {
				return nil, ErrPayloadTooLarge
			}
			return nil, ErrUnreadableBody
		}
		return data, nil
	}
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
