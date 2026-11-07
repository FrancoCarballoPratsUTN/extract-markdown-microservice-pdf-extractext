package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/config"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/consul"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

// fakeExtractor responde de inmediato con un Markdown fijo.
type fakeExtractor struct{}

func (f *fakeExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	return "# markdown", 1, nil
}

// blockingExtractor no retorna hasta que se abre su compuerta.
type blockingExtractor struct {
	gate chan struct{}
}

func (b *blockingExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	<-b.gate
	return "# markdown", 1, nil
}

// testConfig es una configuración con la que el servidor puede arrancar.
func testConfig() config.Config {
	return config.Config{
		Port:            "0",
		MaxUploadMB:     1,
		MaxWorkers:      1,
		QueueSize:       1,
		QueueMaxMB:      1,
		ReadTimeout:     5,
		WriteTimeout:    5,
		ShutdownTimeout: 5,
		MaxPages:        10,
	}
}

// freePort reserva un puerto y lo libera, para que el test pueda alcanzar el
// servidor que acaba de arrancar.
func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("could not release the reserved port: %v", err)
	}
	return strconv.Itoa(port)
}

// waitForServer bloquea hasta que el servidor responde a la sonda de vida.
func waitForServer(t *testing.T, port string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err == nil {
			response.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the server never answered the health probe")
}

// Una señal debe detener el servidor, drenar el pool y terminar sin error:
// ese es todo el contrato de apagado.
func TestServeShutsDownWhenTheSignalArrives(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

	stopCh := make(chan os.Signal, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		stopCh <- syscall.SIGTERM
	}()

	if err := serve(context.Background(), logger, testConfig(), &fakeExtractor{}, stopCh); err != nil {
		t.Fatalf("serve() error = %v, want no error", err)
	}

	logs := output.String()
	for _, want := range []string{"server listening", "shutting down", "draining worker pool", "worker pool drained"} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %q line: %s", want, logs)
		}
	}
}

// Un worker atascado en una llamada nativa no debe mantener el apagado más
// allá del presupuesto configurado: el proceso tiene que salir y decir por qué.
func TestServeGivesUpWhenThePoolDoesNotDrain(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})

	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })

	cfg := testConfig()
	cfg.Port = freePort(t)
	cfg.ShutdownTimeout = 1

	stopCh := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serve(context.Background(), logger, cfg, &blockingExtractor{gate: gate}, stopCh)
	}()

	waitForServer(t, cfg.Port)

	// La petición deja un trabajo atascado en el extractor: el pool no puede drenar.
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+cfg.Port+"/extract", strings.NewReader("%PDF-1.7"))
	if err != nil {
		t.Fatalf("could not build the request: %v", err)
	}
	request.Header.Set("Content-Type", "application/pdf")
	go func() {
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
		}
	}()

	// Dar tiempo al worker para tomar el trabajo antes de pedir el apagado.
	time.Sleep(100 * time.Millisecond)
	stopCh <- syscall.SIGTERM

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("serve() error = nil, want the drain timeout to be reported")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve() did not return")
	}

	logs := output.String()
	for _, want := range []string{"shutting down", "draining worker pool", "worker pool drain did not complete"} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %q line: %s", want, logs)
		}
	}
}

// El servidor tiene que responder a la sonda de vida que usan el orquestador y Consul.
func TestServeAnswersTheHealthProbe(t *testing.T) {
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &bytes.Buffer{}})

	cfg := testConfig()
	cfg.Port = freePort(t)

	stopCh := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serve(context.Background(), logger, cfg, &fakeExtractor{}, stopCh)
	}()

	waitForServer(t, cfg.Port)

	response, err := http.Get("http://127.0.0.1:" + cfg.Port + "/healthz")
	if err != nil {
		t.Fatalf("the health probe failed: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("the health probe body could not be read: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("the health probe body could not be closed: %v", err)
	}

	stopCh <- syscall.SIGTERM
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("serve() error = %v, want no error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve() did not return")
	}
}

func TestServiceRegistrationUsesTheResolvedConsulConfig(t *testing.T) {
	cfg := config.Config{
		Port: "8080",
		Consul: config.ConsulConfig{
			Enabled:         true,
			HTTPAddr:        "http://consul:8500",
			HTTPToken:       "token",
			ServiceName:     "extract",
			ServiceID:       "extract-1",
			ServiceAddress:  "10.0.0.5",
			ServiceCheckURL: "http://10.0.0.5:8080/healthz",
			ServiceTags:     []string{"security-headers@file,extract-cb@file", "metrics"},
		},
	}

	client, registration, err := serviceRegistration(cfg)
	if err != nil {
		t.Fatalf("serviceRegistration() unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("serviceRegistration() client = nil with Consul enabled")
	}

	want := consul.ServiceRegistration{
		ID:      "extract-1",
		Name:    "extract",
		Address: "10.0.0.5",
		Port:    8080,
		Check:   "http://10.0.0.5:8080/healthz",
		Tags:    []string{"security-headers@file,extract-cb@file", "metrics"},
	}
	if !reflect.DeepEqual(registration, want) {
		t.Fatalf("registration = %#v, want %#v", registration, want)
	}
}

func TestServiceRegistrationDisabledReturnsNoClient(t *testing.T) {
	client, registration, err := serviceRegistration(testConfig())
	if err != nil {
		t.Fatalf("serviceRegistration() unexpected error: %v", err)
	}
	if client != nil {
		t.Fatal("serviceRegistration() client must be nil with Consul disabled")
	}
	if !reflect.DeepEqual(registration, consul.ServiceRegistration{}) {
		t.Fatalf("registration = %#v, want the zero value", registration)
	}
}

func TestServiceRegistrationLogsTheEffectiveTags(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewWithOptions(logging.Options{Level: slog.LevelDebug, Output: &output})
	consulServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer consulServer.Close()

	cfg := testConfig()
	cfg.Port = freePort(t)
	cfg.Consul = config.ConsulConfig{
		Enabled:     true,
		HTTPAddr:    consulServer.URL,
		HTTPToken:   "token",
		ServiceID:   "extract-test",
		ServiceTags: []string{"security-headers@file,extract-cb@file", "metrics"},
	}
	stopCh := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serve(context.Background(), logger, cfg, &fakeExtractor{}, stopCh)
	}()

	waitForServer(t, cfg.Port)
	stopCh <- syscall.SIGTERM
	if err := <-result; err != nil {
		t.Fatalf("serve() error = %v, want nil", err)
	}

	logs := output.String()
	if !strings.Contains(logs, `"msg":"registered in Consul"`) {
		t.Fatalf("logs = %s, want Consul registration log", logs)
	}
	if !strings.Contains(logs, "security-headers@file,extract-cb@file") ||
		!strings.Contains(logs, "metrics") {
		t.Errorf("logs = %s, want effective split tags", logs)
	}
}
