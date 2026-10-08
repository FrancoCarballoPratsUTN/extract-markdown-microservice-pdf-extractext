package config

import (
	"testing"
)

func TestLoadDefaultsToMaxWorkers(t *testing.T) {
	t.Setenv("MAX_WORKERS", "")

	cfg := Load()

	if cfg.MaxWorkers != defaultMaxWorkers {
		t.Errorf("MaxWorkers = %d, want %d", cfg.MaxWorkers, defaultMaxWorkers)
	}
}

func TestLoadDefaultsToMaxPagesLimit(t *testing.T) {
	t.Setenv("MAX_PAGES", "")

	cfg := Load()

	if cfg.MaxPages != defaultMaxPages {
		t.Errorf("MaxPages = %d, want %d", cfg.MaxPages, defaultMaxPages)
	}
}

func TestLoadReadsMaxPagesFromEnv(t *testing.T) {
	t.Setenv("MAX_PAGES", "50")

	cfg := Load()

	if cfg.MaxPages != 50 {
		t.Errorf("MaxPages = %d, want 50", cfg.MaxPages)
	}
}

func TestLoadKeepsZeroMaxPagesToDisableLimit(t *testing.T) {
	t.Setenv("MAX_PAGES", "0")

	cfg := Load()

	if cfg.MaxPages != 0 {
		t.Errorf("MaxPages = %d, want 0 (limit disabled)", cfg.MaxPages)
	}
}
