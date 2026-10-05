package pdf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake worker: %v", err)
	}
	return path
}

func TestNewPoolRejectsMissingWorkerPath(t *testing.T) {
	pool, err := NewPool(PoolConfig{Workers: 1})

	if !errors.Is(err, ErrWorkerPathRequired) {
		t.Errorf("error = %v, want ErrWorkerPathRequired", err)
	}
	if pool != nil {
		t.Error("expected nil pool when config is invalid")
	}
}

func TestNewPoolRejectsWorkerPathThatDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "pdfium-worker")

	pool, err := NewPool(PoolConfig{WorkerPath: missing, Workers: 1})

	if !errors.Is(err, ErrWorkerNotFound) {
		t.Errorf("error = %v, want ErrWorkerNotFound", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %v, want it to name the offending path %q", err, missing)
	}
	if pool != nil {
		t.Error("expected nil pool when config is invalid")
	}
}

func TestNewPoolRejectsWorkerPathThatIsNotExecutable(t *testing.T) {
	notExecutable := filepath.Join(t.TempDir(), "pdfium-worker")
	if err := os.WriteFile(notExecutable, []byte("data"), 0o644); err != nil {
		t.Fatalf("write non-executable file: %v", err)
	}

	pool, err := NewPool(PoolConfig{WorkerPath: notExecutable, Workers: 1})

	if !errors.Is(err, ErrWorkerNotExecutable) {
		t.Errorf("error = %v, want ErrWorkerNotExecutable", err)
	}
	if pool != nil {
		t.Error("expected nil pool when config is invalid")
	}
}

func TestNewPoolRejectsNonPositiveWorkerCount(t *testing.T) {
	for _, workers := range []int{0, -1} {
		pool, err := NewPool(PoolConfig{WorkerPath: writeExecutable(t, "pdfium-worker"), Workers: workers})

		if !errors.Is(err, ErrInvalidWorkerCount) {
			t.Errorf("workers = %d: error = %v, want ErrInvalidWorkerCount", workers, err)
		}
		if pool != nil {
			t.Errorf("workers = %d: expected nil pool when config is invalid", workers)
		}
	}
}
