//go:build integration

package pdf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klippa-app/go-pdfium"
)

const (
	testdataDir   = "../../../testdata"
	scrumGuidePDF = "2020-Scrum-Guide-Spanish-Latin-South-American.pdf"
	extractBudget = 60 * time.Second
)

// workerPath resolves the compiled worker binary. The container test stage
// builds it and exports PDFIUM_WORKER_PATH; a local run must build it first
// with: go build -o bin/pdfium-worker ./cmd/pdfium-worker
func workerPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PDFIUM_WORKER_PATH")
	if path == "" {
		t.Skip("PDFIUM_WORKER_PATH is unset: build cmd/pdfium-worker and export its path")
	}
	return path
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func extractCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), extractBudget)
	t.Cleanup(cancel)
	return ctx
}

func newTestPool(t *testing.T) pdfium.Pool {
	t.Helper()
	pool, err := NewPool(PoolConfig{
		WorkerPath:   workerPath(t),
		Workers:      1,
		StartTimeout: extractBudget,
	})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// Regression guard for the defect where the pool was created without a Command,
// so no worker process could be spawned and every request failed with
// "extraction failed" while the server stayed up.
func TestExtractorReturnsTextFromRealPDF(t *testing.T) {
	extractor := NewExtractor(newTestPool(t))

	content, pageCount, err := extractor.Extract(extractCtx(t), readFixture(t, scrumGuidePDF))

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if pageCount != 16 {
		t.Errorf("pageCount = %d, want 16", pageCount)
	}
	if content == "" {
		t.Error("content is empty, want extracted text")
	}
}

func TestExtractorRejectsCorruptPDF(t *testing.T) {
	truncated := readFixture(t, scrumGuidePDF)[:2048]

	_, _, err := NewExtractor(newTestPool(t)).Extract(extractCtx(t), truncated)

	if err == nil {
		t.Fatal("Extract() error = nil, want a corrupt-document error")
	}
}
