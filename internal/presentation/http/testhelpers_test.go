package http

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeLogLine decodes a single JSON log record from the buffer. It fails the
// test if the buffer is empty or not a valid single JSON object.
//
// It lives in an untagged file so every test of this package can use it,
// tagged or not.
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

// decodeLogLines decodes every JSON record of the buffer. A single request can
// produce more than one line: the panic record and the access log line, for
// instance.
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

// findLogRecord returns the record with the given message, failing the test if
// there is none.
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
