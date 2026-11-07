package srvlifecycle

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Option configures a [Lifecycle] during [New].
type Option func(cfg *config) error

// config contains the configuration of a Lifecycle.
type config struct {
	name               string
	logger             *slog.Logger
	shutdownTimeout    time.Duration
	shutdownWaitGroup  *sync.WaitGroup
	shutdownSignalChan <-chan struct{}
	serverClosedFn     func(err error) bool
}

// defaultConfig returns the default configuration.
func defaultConfig() *config {
	return &config{
		name:               "network",
		logger:             slog.Default(),
		shutdownTimeout:    30 * time.Second,
		shutdownWaitGroup:  &sync.WaitGroup{},
		shutdownSignalChan: make(chan struct{}),
		serverClosedFn:     isHTTPServerClosed,
	}
}

// isHTTPServerClosed reports whether err is http.ErrServerClosed.
func isHTTPServerClosed(err error) bool {
	return errors.Is(err, http.ErrServerClosed)
}

// WithName sets the protocol name used in the log messages, for example
// "listening for <name> requests" (default "network").
func WithName(name string) Option {
	return func(cfg *config) error {
		if name == "" {
			return errors.New("name is required")
		}

		cfg.name = name

		return nil
	}
}

// WithLogger overrides the default logger.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) error {
		if logger == nil {
			return errors.New("logger is required")
		}

		cfg.logger = logger

		return nil
	}
}

// WithShutdownTimeout sets the time the shutdown waits for in-flight requests
// when it is triggered by the signal channel, the Start context, or a serve
// failure (default 30 seconds).
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid shutdownTimeout")
		}

		cfg.shutdownTimeout = timeout

		return nil
	}
}

// WithShutdownWaitGroup sets the wait group incremented when the server starts
// and decremented once when it shuts down, so Wait returns immediately if
// called before the server has been started.
func WithShutdownWaitGroup(wg *sync.WaitGroup) Option {
	return func(cfg *config) error {
		if wg == nil {
			return errors.New("shutdownWaitGroup is required")
		}

		cfg.shutdownWaitGroup = wg

		return nil
	}
}

// WithShutdownSignalChan sets the channel that triggers the shutdown when it
// receives a value or is closed. Close it to wake every server that shares it,
// since a single send is delivered to only one receiver.
func WithShutdownSignalChan(ch <-chan struct{}) Option {
	return func(cfg *config) error {
		if ch == nil {
			return errors.New("shutdownSignalChan is required")
		}

		cfg.shutdownSignalChan = ch

		return nil
	}
}

// WithServerClosedFunc sets the function that reports whether a non-nil error
// returned by Serve denotes a normal shutdown, for example
// errors.Is(err, grpc.ErrServerStopped). The default matches
// http.ErrServerClosed. A nil error always denotes a normal shutdown.
func WithServerClosedFunc(fn func(err error) bool) Option {
	return func(cfg *config) error {
		if fn == nil {
			return errors.New("serverClosedFunc is required")
		}

		cfg.serverClosedFn = fn

		return nil
	}
}
