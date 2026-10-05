package pdf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/multi_threaded"
)

// minIdleWorkers keeps one worker warm so the first request does not pay the
// process-spawn and gRPC handshake cost.
const minIdleWorkers = 1

// defaultStartTimeout bounds how long a worker may take to launch and complete
// the go-plugin handshake before startup is considered failed.
const defaultStartTimeout = 30 * time.Second

var (
	ErrWorkerPathRequired  = errors.New("pdfium worker path is required")
	ErrWorkerNotFound      = errors.New("pdfium worker binary not found")
	ErrWorkerNotExecutable = errors.New("pdfium worker binary is not executable")
	ErrInvalidWorkerCount  = errors.New("pdfium worker count must be positive")
	ErrPoolUnavailable     = errors.New("pdfium pool is unavailable")
)

// PoolConfig describes the PDFium worker pool the service depends on. PDFium is
// a C++ library that can crash the hosting process, so it is never linked into
// this binary: every worker is a separate process reached over go-plugin.
type PoolConfig struct {
	// WorkerPath is the path to the pdfium-worker binary. The pool spawns one
	// process per worker, so it must be resolvable at runtime.
	WorkerPath string
	// Workers bounds the total number of concurrent worker processes.
	Workers int
	// StartTimeout bounds worker launch. Zero applies defaultStartTimeout.
	StartTimeout time.Duration
	// OnLogEvent receives pool lifecycle events such as worker exits and failed
	// health pings. Nil discards them.
	OnLogEvent func(string)
}

func (c PoolConfig) startTimeout() time.Duration {
	if c.StartTimeout > 0 {
		return c.StartTimeout
	}
	return defaultStartTimeout
}

// validate rejects an unusable configuration before any process is spawned.
func (c PoolConfig) validate() error {
	if c.WorkerPath == "" {
		return ErrWorkerPathRequired
	}
	if c.Workers <= 0 {
		return ErrInvalidWorkerCount
	}

	info, err := os.Stat(c.WorkerPath)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrWorkerNotFound, c.WorkerPath)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%w: %s", ErrWorkerNotExecutable, c.WorkerPath)
	}
	return nil
}

// NewPool creates a PDFium worker pool and verifies it can hand out an
// instance. Verification happens up front so a missing or unusable worker
// binary fails startup loudly instead of turning every request into a 500.
func NewPool(cfg PoolConfig) (pdfium.Pool, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	startTimeout := cfg.startTimeout()
	pool := multi_threaded.Init(multi_threaded.Config{
		MinIdle:     minIdleWorkers,
		MaxIdle:     cfg.Workers,
		MaxTotal:    cfg.Workers,
		LogCallback: cfg.OnLogEvent,
		Command: multi_threaded.Command{
			BinPath:      cfg.WorkerPath,
			StartTimeout: startTimeout,
		},
	})

	if err := verifyPool(pool, startTimeout); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

// verifyPool borrows and releases an instance to prove the worker launches and
// completes the plugin handshake.
func verifyPool(pool pdfium.Pool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	instance, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPoolUnavailable, err)
	}
	if err := instance.Close(); err != nil {
		return fmt.Errorf("%w: %w", ErrPoolUnavailable, err)
	}
	return nil
}
