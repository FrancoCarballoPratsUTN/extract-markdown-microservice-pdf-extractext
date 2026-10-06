package config

import (
	"os"
	"path/filepath"
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
)

// pdfiumWorkerName is the binary the pool spawns. It lives next to the server
// binary in both the local build and the container image.
const pdfiumWorkerName = "pdfium-worker"

type Config struct {
	Port             string
	MaxUploadMB      int
	MaxWorkers       int
	QueueSize        int
	QueueMaxMB       int // byte budget shared by running and queued jobs
	ReadTimeout      int // seconds
	WriteTimeout     int // seconds
	PdfiumWorkerPath string
}

func Load() *Config {
	return &Config{
		Port:             getEnv("PORT", defaultPort),
		MaxUploadMB:      getEnvInt("MAX_UPLOAD_MB", defaultMaxUploadMB),
		MaxWorkers:       getEnvInt("MAX_WORKERS", defaultMaxWorkers),
		QueueSize:        getEnvInt("QUEUE_SIZE", defaultQueueSize),
		QueueMaxMB:       getEnvInt("QUEUE_MAX_MB", defaultQueueMaxMB),
		ReadTimeout:      getEnvInt("READ_TIMEOUT", defaultReadTimeout),
		WriteTimeout:     getEnvInt("WRITE_TIMEOUT", defaultWriteTimeout),
		PdfiumWorkerPath: getEnv("PDFIUM_WORKER_PATH", defaultWorkerPath(currentExecutable(), pdfiumWorkerName)),
	}
}

// defaultWorkerPath resolves the worker to a sibling of the server binary so a
// plain build produces a runnable pair with no extra configuration.
func defaultWorkerPath(execPath, workerName string) string {
	if dir := filepath.Dir(execPath); dir != "" && dir != "." {
		return filepath.Join(dir, workerName)
	}
	return workerName
}

func currentExecutable() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	return execPath
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
