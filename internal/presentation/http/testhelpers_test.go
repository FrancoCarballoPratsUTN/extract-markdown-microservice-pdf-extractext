package http

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeLogLine decodifica un único registro JSON del buffer. Falla el test si
// el buffer está vacío o no es un único objeto JSON válido.
//
// Vive en un fichero sin tags para que todos los tests de este paquete puedan
// usarlo, tengan tags o no.
func decodeLogLine(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatal("expected a log record, got none")
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log record is not valid JSON: %v (%q)", err, line)
	}
	return record
}

// decodeLogLines decodifica todos los registros JSON del buffer. Una sola
// petición puede producir más de una línea: el registro del panic y la línea
// del access log, por ejemplo.
func decodeLogLines(t *testing.T, raw []byte) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log record is not valid JSON: %v (%q)", err, line)
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		t.Fatal("expected a log record, got none")
	}
	return records
}

// findLogRecord devuelve el registro con el mensaje dado y falla el test si no
// existe.
func findLogRecord(t *testing.T, records []map[string]any, msg string) map[string]any {
	t.Helper()

	for _, record := range records {
		if record["msg"] == msg {
			return record
		}
	}
	t.Fatalf("no log record with msg %q in %v", msg, records)
	return nil
}
