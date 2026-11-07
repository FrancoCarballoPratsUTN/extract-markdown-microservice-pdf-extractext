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

	// multipartOverheadAllowance cubre los boundaries, las cabeceras de parte y
	// el delimitador de cierre. Sin ello, un PDF de exactamente maxBytes sería
	// rechazado sólo por el envoltorio multipart.
	multipartOverheadAllowance = 8 << 10
)

var (
	ErrMissingFile          = errors.New("missing pdf file")
	ErrPayloadTooLarge      = errors.New("payload too large")
	ErrUnsupportedMediaType = errors.New("unsupported media type")
	// ErrUnreadableBody indica que el body no se pudo leer hasta el final. Es
	// distinto de ErrMissingFile: una subida truncada es un problema de
	// transporte, no una petición sin fichero.
	ErrUnreadableBody = errors.New("could not read request body")
)

// readPDFPayload extrae los bytes del PDF de la petición, aceptando tanto una
// subida multipart/form-data en el campo "file" como un body binario crudo.
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

// readMultipartPDF extrae la parte "file", acotada por maxPartBytes, y exige
// que el body completo quepa en maxBodyBytes. El tope del body es mayor porque
// el envoltorio multipart (boundaries y cabeceras de parte) no forma parte del
// documento.
func readMultipartPDF(r *http.Request, maxPartBytes, maxBodyBytes int64) ([]byte, error) {
	// nil es seguro: MaxBytesReader sólo hace un type assertion del writer para
	// marcar la conexión a net/http. El handler escribe el 413.
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

// readFilePart devuelve los bytes de la primera parte llamada "file" que tenga
// nombre de fichero. Las demás partes se omiten.
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

// ensureBodyConsumed drena el resto de la petición para que un body demasiado
// grande se reporte como tal en lugar de ignorarse en silencio.
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

// readLimited lee como máximo maxBytes y devuelve ErrPayloadTooLarge cuando el
// reader contiene más datos de los permitidos.
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
