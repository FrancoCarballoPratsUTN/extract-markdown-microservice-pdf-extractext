package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultPort            = "8080"
	defaultMaxUploadMB     = 50
	defaultMaxWorkers      = 10
	defaultQueueSize       = 16
	defaultQueueMaxMB      = 256
	defaultReadTimeout     = 30
	defaultWriteTimeout    = 30
	defaultShutdownTimeout = 35
	defaultMaxPages        = 1000

	// maxUploadMB and maxQueueMB bound the megabyte values. The byte budget
	// is computed as mb << 20, and an absurd value would overflow that shift
	// into a negative number of bytes: every upload would look empty and
	// every job would be refused.
	maxUploadMB = 1024
	maxQueueMB  = 1024
)

// Config is the effective configuration of the service. The business values
// come from the environment or from the Consul KV; Consul holds the bootstrap
// of that connection, always read from the process environment.
type Config struct {
	Port            string
	MaxUploadMB     int
	MaxWorkers      int
	QueueSize       int
	QueueMaxMB      int // byte budget shared by running and queued jobs
	ReadTimeout     int // seconds
	WriteTimeout    int // seconds
	ShutdownTimeout int // seconds
	MaxPages        int // per-document page cap; 0 disables the limit
	Consul          ConsulConfig
}

// Load reads the configuration from the environment.
//
// An invalid value is an error, not a fallback: a service that quietly starts
// with a configuration nobody asked for hides the misconfiguration until it
// surfaces as a strange behaviour under load. The default only applies when
// the variable is absent.
func Load() (Config, error) {
	return load(os.Getenv)
}

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
		max      int // 0 means no upper bound
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

// setPort reads the listening port. It must be a valid TCP port: an
// unusable value would otherwise surface as a cryptic bind error at boot.
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

// envInt reads an integer from the environment. An absent variable falls back
// to the default; a value that is not an integer, that is below the minimum, or
// that is above the maximum, aborts the startup. A maximum of 0 means there is
// no upper bound.
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
