package config

import (
	"path/filepath"
	"testing"
)

func TestPdfiumWorkerPathDefaultsToSiblingOfServerBinary(t *testing.T) {
	got := defaultWorkerPath("/app/server", pdfiumWorkerName)

	want := filepath.Join("/app", pdfiumWorkerName)
	if got != want {
		t.Errorf("defaultWorkerPath() = %q, want %q", got, want)
	}
}

func TestPdfiumWorkerPathDefaultsToBareNameWhenExecutablePathIsUnknown(t *testing.T) {
	got := defaultWorkerPath("", pdfiumWorkerName)

	if got != pdfiumWorkerName {
		t.Errorf("defaultWorkerPath() = %q, want %q", got, pdfiumWorkerName)
	}
}

func TestLoadUsesWorkerNameWhenExecutablePathIsEmptyDirectory(t *testing.T) {
	got := defaultWorkerPath("server", pdfiumWorkerName)

	want := pdfiumWorkerName
	if got != want {
		t.Errorf("defaultWorkerPath() = %q, want %q", got, want)
	}
}

func TestLoadReadsPdfiumWorkerPathFromEnv(t *testing.T) {
	t.Setenv("PDFIUM_WORKER_PATH", "/custom/pdfium-worker")

	cfg := Load()

	if cfg.PdfiumWorkerPath != "/custom/pdfium-worker" {
		t.Errorf("PdfiumWorkerPath = %q, want %q", cfg.PdfiumWorkerPath, "/custom/pdfium-worker")
	}
}

func TestLoadFallsBackToSiblingOfRunningBinaryWhenEnvIsUnset(t *testing.T) {
	t.Setenv("PDFIUM_WORKER_PATH", "")

	cfg := Load()

	if cfg.PdfiumWorkerPath == "" {
		t.Error("PdfiumWorkerPath is empty, want a resolvable default")
	}
	if filepath.Base(cfg.PdfiumWorkerPath) != pdfiumWorkerName {
		t.Errorf("PdfiumWorkerPath = %q, want it to end in %q", cfg.PdfiumWorkerPath, pdfiumWorkerName)
	}
}

func TestLoadDefaultsToSinglePdfiumWorker(t *testing.T) {
	t.Setenv("MAX_WORKERS", "")

	cfg := Load()

	if cfg.MaxWorkers != defaultMaxWorkers {
		t.Errorf("MaxWorkers = %d, want %d", cfg.MaxWorkers, defaultMaxWorkers)
	}
}
