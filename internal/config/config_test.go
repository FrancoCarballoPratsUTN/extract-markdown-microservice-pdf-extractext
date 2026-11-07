package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsToMaxWorkers(t *testing.T) {
	t.Setenv("MAX_WORKERS", "")

	cfg := loadOrFail(t)

	if cfg.MaxWorkers != defaultMaxWorkers {
		t.Errorf("MaxWorkers = %d, want %d", cfg.MaxWorkers, defaultMaxWorkers)
	}
}

func TestLoadDefaultsToMaxPagesLimit(t *testing.T) {
	t.Setenv("MAX_PAGES", "")

	cfg := loadOrFail(t)

	if cfg.MaxPages != defaultMaxPages {
		t.Errorf("MaxPages = %d, want %d", cfg.MaxPages, defaultMaxPages)
	}
}

func TestLoadReadsMaxPagesFromEnv(t *testing.T) {
	t.Setenv("MAX_PAGES", "50")

	cfg := loadOrFail(t)

	if cfg.MaxPages != 50 {
		t.Errorf("MaxPages = %d, want 50", cfg.MaxPages)
	}

}

func TestLoadFromValuesUsesConsulValues(t *testing.T) {
	cfg, err := LoadFromValues(map[string]string{
		"PORT":        "9090",
		"MAX_WORKERS": "12",
		"MAX_PAGES":   "250",
	})
	if err != nil {
		t.Fatalf("LoadFromValues() unexpected error: %v", err)
	}
	if cfg.Port != "9090" || cfg.MaxWorkers != 12 || cfg.MaxPages != 250 {
		t.Fatalf("config = %#v, want Consul values", cfg)
	}
}

func TestLoadKeepsZeroMaxPagesToDisableLimit(t *testing.T) {
	t.Setenv("MAX_PAGES", "0")

	cfg := loadOrFail(t)

	if cfg.MaxPages != 0 {
		t.Errorf("MaxPages = %d, want 0 (limit disabled)", cfg.MaxPages)
	}
}

func TestLoadReadsShutdownTimeoutFromEnv(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "45")

	cfg := loadOrFail(t)

	if cfg.ShutdownTimeout != 45 {
		t.Errorf("ShutdownTimeout = %d, want 45", cfg.ShutdownTimeout)
	}
}

func TestLoadDefaultsToShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg := loadOrFail(t)

	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %d, want %d", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
}

func TestLoadFailsOnAnInvalidValue(t *testing.T) {
	t.Setenv("MAX_WORKERS", "many")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for a non-integer MAX_WORKERS")
	}
	if !strings.Contains(err.Error(), "MAX_WORKERS") {
		t.Errorf("Load() error = %v, want it to name the offending variable", err)
	}
}

func TestLoadFailsOnAValueBelowTheMinimum(t *testing.T) {
	t.Setenv("MAX_WORKERS", "0")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for MAX_WORKERS=0")
	}
}

func TestLoadFailsOnAnInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-port")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for a non-integer PORT")
	}
}

func TestLoadFailsOnAPortOutsideTheValidRange(t *testing.T) {
	t.Setenv("PORT", "70000")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for PORT=70000")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("Load() error = %v, want it to name the offending variable", err)
	}
}

// El presupuesto en bytes se calcula como mb << 20. Sin un tope superior, un
// valor absurdo desborda el desplazamiento a un número negativo de bytes y toda
// subida parecería vacía.
func TestLoadFailsOnAnUploadLimitAboveTheMaximum(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "9000000000000")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for an absurd MAX_UPLOAD_MB")
	}
	if !strings.Contains(err.Error(), "MAX_UPLOAD_MB") {
		t.Errorf("Load() error = %v, want it to name the offending variable", err)
	}
}

func TestLoadFailsOnAQueueLimitAboveTheMaximum(t *testing.T) {
	t.Setenv("QUEUE_MAX_MB", "9000000000000")

	_, err := Load()

	if err == nil {
		t.Fatal("Load() error = nil, want an error for an absurd QUEUE_MAX_MB")
	}
	if !strings.Contains(err.Error(), "QUEUE_MAX_MB") {
		t.Errorf("Load() error = %v, want it to name the offending variable", err)
	}
}

func loadOrFail(t *testing.T) Config {
	t.Helper()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want no error", err)
	}
	return cfg
}
