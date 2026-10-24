package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/config"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
)

// fakeExtractor answers immediately with a fixed Markdown.
type fakeExtractor struct{}

func (f *fakeExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	return "# markdown", 1, nil
}

// blockingExtractor never returns until its gate is opened.
type blockingExtractor struct {
	gate chan struct{}
}

func (b *blockingExtractor) Extract(ctx context.Context, data []byte) (string, int, error) {
	<-b.gate
	return "# markdown", 1, nil
}

// testConfig is a configuration the server can actually run.
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

// freePort reserves a port and gives it back, so the test can reach the
// server it just started.
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

// waitForServer blocks until the server answers the health probe.
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

// A signal has to stop the server, drain the pool and leave with no error:
// that is the whole shutdown contract.
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

// A worker stuck in a native call must not keep the shutdown waiting past the
// configured budget: the process has to exit and say why.
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

	// The request leaves a job stuck in the extractor: the pool cannot drain.
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

	// Give the worker time to pick the job up before asking for the shutdown.
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

// The server has to answer the health probe: it is what the orchestrator and
// the container healthcheck use to decide whether the replica is alive.
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
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
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
