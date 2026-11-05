package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// consulValues devuelve un getenv con la conexión mínima habilitada; los
// overrides permiten probar cada variable sin repetir el resto.
func consulValues(overrides map[string]string) func(string) string {
	values := map[string]string{
		"CONSUL_ENABLED":    "true",
		"CONSUL_HTTP_ADDR":  "http://consul:8500",
		"CONSUL_HTTP_TOKEN": "token",
		"CONSUL_KV_PREFIX":  "pdf-extractext/dev/extract/",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return func(key string) string { return values[key] }
}

func TestLoadConsulIsDisabledByDefault(t *testing.T) {
	cfg, err := loadConsul(func(string) string { return "" })
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Consul must be disabled when CONSUL_ENABLED is absent")
	}
}

func TestLoadConsulOnlyAcceptsTrue(t *testing.T) {
	for _, raw := range []string{"false", "yes", "1", "TRUE"} {
		cfg, err := loadConsul(consulValues(map[string]string{"CONSUL_ENABLED": raw}))
		if err != nil {
			t.Fatalf("loadConsul(CONSUL_ENABLED=%q) unexpected error: %v", raw, err)
		}
		if want := raw == "TRUE"; cfg.Enabled != want {
			t.Errorf("loadConsul(CONSUL_ENABLED=%q).Enabled = %v, want %v", raw, cfg.Enabled, want)
		}
	}
}

func TestLoadConsulEnabledRequiresConnectionValues(t *testing.T) {
	for _, name := range []string{"CONSUL_HTTP_ADDR", "CONSUL_HTTP_TOKEN", "CONSUL_KV_PREFIX"} {
		_, err := loadConsul(consulValues(map[string]string{name: "  "}))
		if err == nil {
			t.Fatalf("loadConsul() error = nil with %s blank, want an error", name)
		}
		if !strings.Contains(err.Error(), "CONSUL_HTTP_ADDR, CONSUL_HTTP_TOKEN and CONSUL_KV_PREFIX") {
			t.Errorf("error = %v, want it to name the required variables", err)
		}
	}
}

func TestLoadConsulTrimsConnectionValues(t *testing.T) {
	cfg, err := loadConsul(consulValues(map[string]string{
		"CONSUL_HTTP_ADDR":    "  http://consul:8500  ",
		"CONSUL_HTTP_TOKEN":   "  secret  ",
		"CONSUL_KV_PREFIX":    "  prefix/  ",
		"CONSUL_HTTP_CA_FILE": "/etc/ssl/consul.pem",
	}))
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}
	if cfg.HTTPAddr != "http://consul:8500" || cfg.HTTPToken != "secret" || cfg.KVPrefix != "prefix/" {
		t.Errorf("connection values = %#v, want trimmed values", cfg)
	}
	if cfg.HTTPCAFile != "/etc/ssl/consul.pem" {
		t.Errorf("HTTPCAFile = %q, want the raw path", cfg.HTTPCAFile)
	}
}

func TestLoadConsulSplitsServiceTags(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"metrics", []string{"metrics"}},
		{"a,b", []string{"a,b"}},
		{"a;b", []string{"a", "b"}},
		{" a ; ; b ", []string{"a", "b"}},
		{"a;b,c", []string{"a", "b,c"}},
	}
	for _, tt := range tests {
		cfg, err := loadConsul(consulValues(map[string]string{"CONSUL_SERVICE_TAGS": tt.raw}))
		if err != nil {
			t.Fatalf("loadConsul() unexpected error: %v", err)
		}
		if !reflect.DeepEqual(cfg.ServiceTags, tt.want) {
			t.Errorf("ServiceTags for %q = %#v, want %#v", tt.raw, cfg.ServiceTags, tt.want)
		}
	}
}

func TestLoadConsulReadsTheEnvironment(t *testing.T) {
	t.Setenv("CONSUL_ENABLED", "true")
	t.Setenv("CONSUL_HTTP_ADDR", "http://consul:8500")
	t.Setenv("CONSUL_HTTP_TOKEN", "token")
	t.Setenv("CONSUL_KV_PREFIX", "prefix/")
	t.Setenv("CONSUL_SERVICE_TAGS", "security-headers@file,extract-cb@file;metrics")

	cfg, err := LoadConsul()
	if err != nil {
		t.Fatalf("LoadConsul() unexpected error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("LoadConsul().Enabled = false, want true")
	}
	want := []string{"security-headers@file,extract-cb@file", "metrics"}
	if !reflect.DeepEqual(cfg.ServiceTags, want) {
		t.Errorf("ServiceTags = %#v, want %#v", cfg.ServiceTags, want)
	}
}

func TestConsulServiceRegistrationAppliesDefaults(t *testing.T) {
	cfg, err := loadConsul(consulValues(map[string]string{"CONSUL_SERVICE_ADDRESS": "10.0.0.5"}))
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}

	registration, err := cfg.ServiceRegistration("8080")
	if err != nil {
		t.Fatalf("ServiceRegistration() unexpected error: %v", err)
	}

	want := ConsulServiceRegistration{
		ID:      "extract-10.0.0.5",
		Name:    "extract",
		Address: "10.0.0.5",
		Port:    8080,
		Check:   "http://10.0.0.5:8080/healthz",
	}
	if !reflect.DeepEqual(registration, want) {
		t.Errorf("registration = %#v, want %#v", registration, want)
	}
}

func TestConsulServiceRegistrationKeepsExplicitValues(t *testing.T) {
	cfg, err := loadConsul(consulValues(map[string]string{
		"CONSUL_SERVICE_NAME":      "pdf",
		"CONSUL_SERVICE_ID":        "pdf-1",
		"CONSUL_SERVICE_ADDRESS":   "10.0.0.9",
		"CONSUL_SERVICE_CHECK_URL": "http://health/ready",
		"CONSUL_SERVICE_TAGS":      "a;b",
	}))
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}

	registration, err := cfg.ServiceRegistration("9090")
	if err != nil {
		t.Fatalf("ServiceRegistration() unexpected error: %v", err)
	}

	want := ConsulServiceRegistration{
		ID:      "pdf-1",
		Name:    "pdf",
		Address: "10.0.0.9",
		Port:    9090,
		Check:   "http://health/ready",
		Tags:    []string{"a", "b"},
	}
	if !reflect.DeepEqual(registration, want) {
		t.Errorf("registration = %#v, want %#v", registration, want)
	}
}

func TestConsulServiceRegistrationFallsBackToHostname(t *testing.T) {
	cfg, err := loadConsul(consulValues(nil))
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}

	registration, err := cfg.ServiceRegistration("8080")
	if err != nil {
		t.Fatalf("ServiceRegistration() unexpected error: %v", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() unexpected error: %v", err)
	}
	if registration.Address != hostname {
		t.Errorf("Address = %q, want the hostname %q", registration.Address, hostname)
	}
	if registration.ID != "extract-"+hostname {
		t.Errorf("ID = %q, want %q", registration.ID, "extract-"+hostname)
	}
}

func TestConsulServiceRegistrationRejectsAnInvalidPort(t *testing.T) {
	cfg, err := loadConsul(consulValues(nil))
	if err != nil {
		t.Fatalf("loadConsul() unexpected error: %v", err)
	}

	if _, err := cfg.ServiceRegistration("http"); err == nil ||
		!strings.Contains(err.Error(), "invalid service port") {
		t.Errorf("ServiceRegistration(http) error = %v, want an invalid port error", err)
	}
}
