package consul

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKVLoadPrefixDecodesValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recurse") != "true" {
			t.Fatal("recurse query parameter is required")
		}
		if r.Header.Get("X-Consul-Token") != "service-token" {
			t.Fatalf("X-Consul-Token = %q, want service-token", r.Header.Get("X-Consul-Token"))
		}
		_, _ = w.Write([]byte(`[
			{"Key":"pdf-extractext/dev/extract/MAX_WORKERS","Value":"MTA="},
			{"Key":"pdf-extractext/dev/extract/MAX_PAGES","Value":"MTAwMA=="}
		]`))
	}))
	defer server.Close()

	values, err := NewKVClient(server.URL, "service-token", server.Client()).
		LoadPrefix("pdf-extractext/dev/extract/")
	if err != nil {
		t.Fatalf("LoadPrefix() unexpected error: %v", err)
	}
	if values["MAX_WORKERS"] != "10" || values["MAX_PAGES"] != "1000" {
		t.Fatalf("values = %#v, want decoded configuration", values)
	}
}
