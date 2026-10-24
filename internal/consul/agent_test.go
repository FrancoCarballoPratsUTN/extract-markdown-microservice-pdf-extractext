package consul

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterAndDeregisterService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "service-token" {
			t.Fatal("missing Consul token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewKVClient(server.URL, "service-token", server.Client())
	registration := ServiceRegistration{
		ID: "extract-1", Name: "extract", Address: "extract-1", Port: 8080,
		Check: "http://extract-1:8080/healthz",
	}
	if err := client.RegisterService(registration); err != nil {
		t.Fatalf("RegisterService() unexpected error: %v", err)
	}
	if err := client.DeregisterService(registration.ID); err != nil {
		t.Fatalf("DeregisterService() unexpected error: %v", err)
	}
}
