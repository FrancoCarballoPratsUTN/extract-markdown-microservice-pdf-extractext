package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const (
	defaultLevel  = "info"
	defaultFormat = "json"
)

// Options controla la construcción del logger. Format acepta "json" (default)
// y "text". Output escribe a la salida elegida (stdout en producción).
type Options struct {
	Level  slog.Level
	Format string
	Output io.Writer
}

// New construye el logger de producción leyendo LOG_LEVEL y LOG_FORMAT del
// entorno. La configuración del log vive en este paquete a propósito, separada
// de internal/config, que se reserva para las variables de negocio.
func New() (*slog.Logger, error) {
	rawLevel := os.Getenv("LOG_LEVEL")
	if rawLevel == "" {
		rawLevel = defaultLevel
	}
	level, err := ParseLevel(rawLevel)
	if err != nil {
		return nil, err
	}

	format := os.Getenv("LOG_FORMAT")
	if format == "" {
		format = defaultFormat
	}
	if err := ValidateFormat(format); err != nil {
		return nil, err
	}

	return NewWithOptions(Options{Level: level, Format: format, Output: os.Stdout}), nil
}

// NewWithOptions construye un logger explícito (tests, variantes de salida).
func NewWithOptions(opts Options) *slog.Logger {
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	handlerOptions := &slog.HandlerOptions{Level: opts.Level}
	switch opts.Format {
	case "text":
		return slog.New(slog.NewTextHandler(opts.Output, handlerOptions))
	default:
		return slog.New(slog.NewJSONHandler(opts.Output, handlerOptions))
	}
}

// Discard devuelve un logger que descarta todo, para tests que no inspeccionan
// logs. Reemplaza los discardLogger duplicados de cada paquete.
func Discard() *slog.Logger {
	return NewWithOptions(Options{Level: slog.LevelError, Output: io.Discard})
}

// ParseLevel traduce un nombre de nivel a slog.Level. Un valor inválido (o
// vacío) devuelve un error explícito: el default "info" lo aplica New, no esta
// función, para que ninguna llamada se trague un typo.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error; got %q", raw)
	}
}

func ValidateFormat(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("LOG_FORMAT must be one of json, text; got %q", raw)
	}
}
