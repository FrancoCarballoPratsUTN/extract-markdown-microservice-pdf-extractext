package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultPort        = "8080"
	defaultMaxUploadMB = 50
	// 2 workers y 56 de cola son el óptimo medido con la cuota del despliegue
	// (cpus: 1.0 por réplica): mismo throughput, sin 503 y la mitad de memoria
	// que con 10/16. Ver INFORME.md (§4.2 y §5.2).
	defaultMaxWorkers      = 2
	defaultQueueSize       = 56
	defaultQueueMaxMB      = 256
	defaultReadTimeout     = 30
	defaultWriteTimeout    = 30
	defaultShutdownTimeout = 35
	defaultMaxPages        = 1000

	// maxUploadMB y maxQueueMB acotan los valores en megabytes. El presupuesto
	// en bytes se calcula como mb << 20, y un valor absurdo desbordaría ese
	// desplazamiento a un número negativo de bytes: toda subida parecería vacía
	// y todo trabajo sería rechazado.
	maxUploadMB = 1024
	maxQueueMB  = 1024
)

// Config es la configuración efectiva del servicio. Los valores de negocio
// provienen del entorno o del KV de Consul; Consul guarda el bootstrap de esa
// conexión, que siempre se lee del entorno del proceso.
type Config struct {
	Port            string
	MaxUploadMB     int
	MaxWorkers      int
	QueueSize       int
	QueueMaxMB      int // presupuesto en bytes compartido por trabajos en curso y en cola
	ReadTimeout     int // segundos
	WriteTimeout    int // segundos
	ShutdownTimeout int // segundos
	MaxPages        int // tope de páginas por documento; 0 desactiva el límite
	Consul          ConsulConfig
}

// Load lee la configuración del entorno.
//
// Un valor inválido es un error, no un fallback: un servicio que arranca en
// silencio con una configuración que nadie pidió oculta la mala configuración
// hasta que aparece como un comportamiento extraño bajo carga. El default sólo
// se aplica cuando la variable está ausente.
func Load() (Config, error) {
	return load(os.Getenv)
}

// LoadFromValues lee la configuración de un mapa de valores, como el que
// devuelve el KV de Consul. Aplica las mismas reglas y defaults que Load.
func LoadFromValues(values map[string]string) (Config, error) {
	return load(func(key string) string {
		return values[key]
	})
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{}

	if err := setPort(&cfg, getenv); err != nil {
		return Config{}, err
	}

	fields := []struct {
		name     string
		target   *int
		fallback int
		min      int
		max      int // 0 significa sin tope superior
	}{
		{"MAX_UPLOAD_MB", &cfg.MaxUploadMB, defaultMaxUploadMB, 1, maxUploadMB},
		{"MAX_WORKERS", &cfg.MaxWorkers, defaultMaxWorkers, 1, 0},
		{"QUEUE_SIZE", &cfg.QueueSize, defaultQueueSize, 1, 0},
		{"QUEUE_MAX_MB", &cfg.QueueMaxMB, defaultQueueMaxMB, 1, maxQueueMB},
		{"READ_TIMEOUT", &cfg.ReadTimeout, defaultReadTimeout, 1, 0},
		{"WRITE_TIMEOUT", &cfg.WriteTimeout, defaultWriteTimeout, 1, 0},
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout, defaultShutdownTimeout, 1, 0},
		{"MAX_PAGES", &cfg.MaxPages, defaultMaxPages, 0, 0},
	}
	for _, field := range fields {
		value, err := envInt(getenv, field.name, field.fallback, field.min, field.max)
		if err != nil {
			return Config{}, err
		}
		*field.target = value
	}

	return cfg, nil
}

// setPort lee el puerto de escucha. Debe ser un puerto TCP válido: un valor
// inutilizable aparecería de otro modo como un error críptico de bind al
// arrancar.
func setPort(cfg *Config, getenv func(string) string) error {
	raw := getenv("PORT")
	if raw == "" {
		cfg.Port = defaultPort
		return nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("PORT: %q is not an integer", raw)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("PORT: %d is outside the valid range 1-65535", port)
	}
	cfg.Port = raw
	return nil
}

// envInt lee un entero del entorno. Una variable ausente usa el default; un
// valor que no sea entero, que esté por debajo del mínimo o por encima del
// máximo aborta el arranque. Un máximo de 0 significa que no hay tope superior.
func envInt(getenv func(string) string, name string, fallback, min, max int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", name, raw)
	}
	if value < min {
		return 0, fmt.Errorf("%s: %d is below the minimum of %d", name, value, min)
	}
	if max > 0 && value > max {
		return 0, fmt.Errorf("%s: %d is above the maximum of %d", name, value, max)
	}
	return value, nil
}
