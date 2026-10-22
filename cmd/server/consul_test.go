package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadConfigUsesConsulWhenEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "consul-token" {
			t.Fatalf("X-Consul-Token = %q, want consul-token", r.Header.Get("X-Consul-Token"))
		}
		_, _ = w.Write([]byte(`[
			{"Key":"pdf-extractext/dev/extract/PORT","Value":"OTA5MA=="},
			{"Key":"pdf-extractext/dev/extract/MAX_WORKERS","Value":"MTI="},
			{"Key":"pdf-extractext/dev/extract/MAX_PAGES","Value":"MjUw"}
		]`))
	}))
	defer server.Close()

	t.Setenv("CONSUL_ENABLED", "true")
	t.Setenv("CONSUL_HTTP_ADDR", server.URL)
	t.Setenv("CONSUL_HTTP_TOKEN", "consul-token")
	t.Setenv("CONSUL_KV_PREFIX", "pdf-extractext/dev/extract/")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() unexpected error: %v", err)
	}
	if cfg.Port != "9090" || cfg.MaxWorkers != 12 || cfg.MaxPages != 250 {
		t.Fatalf("config = %#v, want Consul values", cfg)
	}
}
