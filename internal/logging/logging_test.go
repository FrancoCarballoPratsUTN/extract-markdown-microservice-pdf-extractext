package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevelValidValues(t *testing.T) {
	t.Parallel()

	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for raw, want := range cases {
		raw, want := raw, want
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLevel(raw)
			if err != nil {
				t.Fatalf("ParseLevel(%q) unexpected error: %v", raw, err)
			}
			if got != want {
				t.Errorf("ParseLevel(%q) = %v, want %v", raw, got, want)
			}
		})
	}
}

func TestParseLevelIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	if got, err := ParseLevel("DEBUG"); err != nil || got != slog.LevelDebug {
		t.Errorf("ParseLevel(DEBUG) = (%v, %v), want (%v, nil)", got, err, slog.LevelDebug)
	}
	if got, err := ParseLevel("Info"); err != nil || got != slog.LevelInfo {
		t.Errorf("ParseLevel(Info) = (%v, %v), want (%v, nil)", got, err, slog.LevelInfo)
	}
}

func TestParseLevelInvalid(t *testing.T) {
	t.Parallel()

	cases := []string{"", "trace", "verbose", "1"}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseLevel(raw); err == nil {
				t.Errorf("ParseLevel(%q) expected an error", raw)
			}
		})
	}
}

func TestNewDefaultLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "text")

	logger, err := New()
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	if logger == nil {
		t.Fatal("New() returned a nil logger")
	}
}

func TestNewInvalidLevelFails(t *testing.T) {
	t.Setenv("LOG_LEVEL", "nope")
	if _, err := New(); err == nil {
		t.Fatal("New() expected an error for an invalid LOG_LEVEL")
	}
}

func TestNewInvalidFormatFails(t *testing.T) {
	t.Setenv("LOG_FORMAT", "yaml")
	if _, err := New(); err == nil {
		t.Fatal("New() expected an error for an invalid LOG_FORMAT")
	}
}

func TestValidateFormatAcceptsSupportedValues(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"json", "text", " JSON ", "TEXT"} {
		if err := ValidateFormat(raw); err != nil {
			t.Errorf("ValidateFormat(%q) unexpected error: %v", raw, err)
		}
	}
}

func TestNewWithOptionsWritesJSONByDefault(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := NewWithOptions(Options{Level: slog.LevelInfo, Output: &output})

	logger.Info("hello")

	got := output.String()
	if !strings.HasPrefix(got, "{") {
		t.Errorf("JSON handler output = %q, want it to start with '{'", got)
	}
	if !strings.Contains(got, `"msg":"hello"`) {
		t.Errorf("JSON handler output = %q, want it to contain msg=hello", got)
	}
}

func TestNewWithOptionsWritesTextWhenRequested(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := NewWithOptions(Options{Level: slog.LevelInfo, Format: "text", Output: &output})

	logger.Info("hello", "k", "v")

	got := output.String()
	if strings.HasPrefix(got, "{") {
		t.Errorf("text handler output = %q, want it NOT to start with '{'", got)
	}
	if !strings.Contains(got, "msg=hello") {
		t.Errorf("text handler output = %q, want it to contain msg=hello", got)
	}
	if !strings.Contains(got, "k=v") {
		t.Errorf("text handler output = %q, want it to contain k=v", got)
	}
}

func TestNewWithOptionsFiltersByLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := NewWithOptions(Options{Level: slog.LevelError, Output: &output})

	logger.Info("should be dropped")
	if got := output.String(); got != "" {
		t.Errorf("output after Info = %q, want empty (level filter)", got)
	}

	logger.Error("boom")
	if !strings.Contains(output.String(), "boom") {
		t.Errorf("output after Error = %q, want it to contain the message", output.String())
	}
}

func TestDiscardReturnsLogger(t *testing.T) {
	t.Parallel()

	if logger := Discard(); logger == nil {
		t.Fatal("Discard() returned nil")
	}
}
