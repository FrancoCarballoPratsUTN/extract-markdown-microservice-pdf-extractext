package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port         string
	MaxUploadMB  int
	MaxWorkers   int
	QueueSize    int
	ReadTimeout  int // seconds
	WriteTimeout int // seconds
}

func Load() *Config {
	cfg := &Config{
		Port:         getEnv("PORT", "8080"),
		MaxUploadMB:  getEnvInt("MAX_UPLOAD_MB", 50),
		MaxWorkers:   getEnvInt("MAX_WORKERS", 10),
		QueueSize:    getEnvInt("QUEUE_SIZE", 100),
		ReadTimeout:  getEnvInt("READ_TIMEOUT", 30),
		WriteTimeout: getEnvInt("WRITE_TIMEOUT", 30),
	}
	return cfg
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}
