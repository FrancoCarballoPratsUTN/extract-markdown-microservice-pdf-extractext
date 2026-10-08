package config

import (
	"os"
	"strconv"
)

const (
	defaultPort         = "8080"
	defaultMaxUploadMB  = 50
	defaultMaxWorkers   = 10
	defaultQueueSize    = 16
	defaultQueueMaxMB   = 256
	defaultReadTimeout  = 30
	defaultWriteTimeout = 30
	defaultMaxPages     = 1000
)

type Config struct {
	Port         string
	MaxUploadMB  int
	MaxWorkers   int
	QueueSize    int
	QueueMaxMB   int // byte budget shared by running and queued jobs
	ReadTimeout  int // seconds
	WriteTimeout int // seconds
	MaxPages     int // per-document page cap; 0 disables the limit
}

func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", defaultPort),
		MaxUploadMB:  getEnvInt("MAX_UPLOAD_MB", defaultMaxUploadMB),
		MaxWorkers:   getEnvInt("MAX_WORKERS", defaultMaxWorkers),
		QueueSize:    getEnvInt("QUEUE_SIZE", defaultQueueSize),
		QueueMaxMB:   getEnvInt("QUEUE_MAX_MB", defaultQueueMaxMB),
		ReadTimeout:  getEnvInt("READ_TIMEOUT", defaultReadTimeout),
		WriteTimeout: getEnvInt("WRITE_TIMEOUT", defaultWriteTimeout),
		MaxPages:     getEnvInt("MAX_PAGES", defaultMaxPages),
	}
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
