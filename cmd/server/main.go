package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/config"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/consul"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/domain"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/infrastructure/pdf"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/logging"
	httppres "github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/presentation/http"
	"github.com/francocp12/extract-markdown-microservice-pdf-extractext/internal/usecase"
)

func main() {
	logger, err := logging.New()
	if err != nil {
		// El logger no se pudo construir: stderr es lo único que queda.
		fmt.Fprintf(os.Stderr, "invalid logging configuration: %v\n", err)
		os.Exit(1)
	}
	if err := run(logger); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logStartup(logger, cfg)

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopCh)

	extractor, err := buildExtractor(cfg, logger)
	if err != nil {
		return err
	}

	return serve(context.Background(), logger, cfg, extractor, stopCh)
}

func loadConfig() (config.Config, error) {
	if !strings.EqualFold(os.Getenv("CONSUL_ENABLED"), "true") {
		return config.Load()
	}

	baseURL := strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR"))
	token := strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN"))
	prefix := strings.TrimSpace(os.Getenv("CONSUL_KV_PREFIX"))
	if baseURL == "" || token == "" || prefix == "" {
		return config.Config{}, fmt.Errorf("CONSUL_HTTP_ADDR, CONSUL_HTTP_TOKEN and CONSUL_KV_PREFIX are required when CONSUL_ENABLED=true")
	}

	client, err := consul.NewKVClientWithCA(baseURL, token, os.Getenv("CONSUL_HTTP_CA_FILE"))
	if err != nil {
		return config.Config{}, err
	}
	values, err := client.LoadPrefix(prefix)
	if err != nil {
		return config.Config{}, err
	}
	return config.LoadFromValues(values)
}

// serve levanta el servidor con el extractor ya construido y lo apaga al
// recibir una señal en stopCh.
//
// El extractor llega por parámetro y no se construye aquí: la construcción
// incluye una prueba de arranque que necesita la librería nativa, y así esta
// función —la que decide cómo se apaga el servicio— se puede testear sin ella.
func serve(ctx context.Context, logger *slog.Logger, cfg config.Config, extractor usecase.Extractor, stopCh <-chan os.Signal) error {
	writeTimeout := time.Duration(cfg.WriteTimeout) * time.Second
	jobs := usecase.NewWorkerPool(
		usecase.NewExtractUsecase(extractor),
		cfg.MaxWorkers,
		cfg.QueueSize,
		int64(cfg.QueueMaxMB)<<20,
		logger,
	)

	extractHandler := httppres.NewHandler(usecase.NewQueuedExtractor(jobs, logger), cfg.MaxUploadMB, logger)
	// La admisión limita los trabajos en curso más los que esperan en la cola:
	// es exactamente el número de trabajos que el pool puede sostener.
	admission := httppres.NewAdmissionMiddleware(cfg.MaxWorkers + cfg.QueueSize)
	protected := admission.Middleware(httppres.WithDeadline(writeTimeout)(extractHandler))

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      httppres.Routes(protected, logger),
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: writeTimeout,
	}
	consulClient, registration, err := serviceRegistration(cfg)
	if err != nil {
		return err
	}
	deregister := func() {}
	if consulClient != nil {
		if err := consulClient.RegisterService(registration); err != nil {
			return err
		}
		deregistered := false
		deregister = func() {
			if deregistered {
				return
			}
			if err := consulClient.DeregisterService(registration.ID); err != nil {
				logger.Warn("Consul service deregistration failed", "error", err)
				return
			}
			deregistered = true
			logger.Info("deregistered from Consul", "service_id", registration.ID)
		}
		// Fallback para las salidas que no pasan por el apagado normal, como un
		// error al abrir el listener.
		defer deregister()
	}

	// El listener se abre antes de loguear: "server listening" tiene que
	// significar que el puerto ya está tomado, no que se espera que lo esté.
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		// La dirección real del listener, no la configurada: con el puerto 0
		// el sistema elige uno y el log tiene que decir cuál.
		logger.Info("server listening", "addr", listener.Addr().String())
		errCh <- srv.Serve(listener)
	}()

	var serveErr error
	select {
	case serveErr = <-errCh:
	case sig := <-stopCh:
		logger.Info("shutting down", "signal", sig.String())
		// Un segundo SIGTERM/SIGINT durante el apagado fuerza la salida
		// inmediata sin esperar a drenar conexiones ni trabajos en vuelo.
		go func() {
			sig2 := <-stopCh
			logger.Error("forced shutdown by second signal", "signal", sig2.String())
			os.Exit(1)
		}()
	}

	// Dejar de anunciarse antes de empezar a drenar: una instancia que va a
	// cerrarse no debe seguir recibiendo tráfico nuevo. El defer de arriba
	// queda como fallback y no repite la llamada si ésta tuvo éxito.
	deregister()

	// Un único presupuesto (SHUTDOWN_TIMEOUT) rige ambos pasos: dejar de aceptar
	// conexiones y drenar los trabajos en vuelo. Si el primero lo agota, el
	// segundo no tiene budget y el proceso igual termina: nunca se cuelga más
	// allá del deadline configurado.
	shutdownCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()

	var shutdownErr error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown did not complete", "error", err)
		shutdownErr = err
	}

	logger.Info("draining worker pool")
	if err := jobs.CloseContext(shutdownCtx); err != nil {
		logger.Warn("worker pool drain did not complete", "error", err)
		if shutdownErr == nil {
			shutdownErr = err
		}
	} else {
		logger.Info("worker pool drained")
	}

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return shutdownErr
}

func serviceRegistration(cfg config.Config) (*consul.KVClient, consul.ServiceRegistration, error) {
	if !strings.EqualFold(os.Getenv("CONSUL_ENABLED"), "true") {
		return nil, consul.ServiceRegistration{}, nil
	}
	port, err := strconv.Atoi(cfg.Port)
	if err != nil {
		return nil, consul.ServiceRegistration{}, fmt.Errorf("invalid service port %q: %w", cfg.Port, err)
	}
	address := strings.TrimSpace(os.Getenv("CONSUL_SERVICE_ADDRESS"))
	if address == "" {
		address, err = os.Hostname()
		if err != nil {
			return nil, consul.ServiceRegistration{}, fmt.Errorf("resolve service hostname: %w", err)
		}
	}
	name := os.Getenv("CONSUL_SERVICE_NAME")
	if name == "" {
		name = "extract"
	}
	id := strings.TrimSpace(os.Getenv("CONSUL_SERVICE_ID"))
	if id == "" {
		id = name + "-" + address
	}
	check := os.Getenv("CONSUL_SERVICE_CHECK_URL")
	if check == "" {
		check = "http://" + address + ":" + cfg.Port + "/healthz"
	}
	var tags []string
	// Aceptar ';' o ',' como separador. Los tags permiten pasar labels/middlewares
	// y patrones de routing al registro de Consul.
	for _, tag := range strings.Split(strings.ReplaceAll(os.Getenv("CONSUL_SERVICE_TAGS"), ",", ";"), ";") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	client, err := consul.NewKVClientWithCA(
		os.Getenv("CONSUL_HTTP_ADDR"),
		os.Getenv("CONSUL_HTTP_TOKEN"),
		os.Getenv("CONSUL_HTTP_CA_FILE"),
	)
	if err != nil {
		return nil, consul.ServiceRegistration{}, err
	}
	return client, consul.ServiceRegistration{
		ID: id, Name: name, Address: address, Port: port, Check: check, Tags: tags,
	}, nil
}

// buildExtractor wires the pdf_oxide-backed extractor and proves the native
// library is loadable before the server accepts traffic.
func buildExtractor(cfg config.Config, logger *slog.Logger) (usecase.Extractor, error) {
	extractor := pdf.NewExtractor(cfg.MaxPages, logger)
	// Boot probe: the engine rejecting an invalid payload proves the native
	// library loaded; any other failure must stop the server before traffic.
	if _, _, err := extractor.Extract(context.Background(), []byte("startup probe")); err != nil &&
		!errors.Is(err, domain.ErrInvalidPDF) {
		return nil, fmt.Errorf("pdf_oxide unavailable: %w", err)
	}
	return extractor, nil
}

// logStartup registra la configuración efectiva del servicio al arrancar.
// No hay secretos en esta configuración, así que se registra completa: los
// límites son justo lo que hace falta para explicar un 413 o un 503.
func logStartup(logger *slog.Logger, cfg config.Config) {
	logger.Info("application starting",
		"addr", ":"+cfg.Port,
		"max_upload_mb", cfg.MaxUploadMB,
		"max_workers", cfg.MaxWorkers,
		"queue_size", cfg.QueueSize,
		"queue_max_mb", cfg.QueueMaxMB,
		"read_timeout", cfg.ReadTimeout,
		"write_timeout", cfg.WriteTimeout,
		"shutdown_timeout", cfg.ShutdownTimeout,
		"max_pages", cfg.MaxPages,
	)
}
