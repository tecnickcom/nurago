/*
Package srvlifecycle runs a network server in the background and shuts it down
gracefully on a signal, on context cancelation, or on an explicit call.

A [Lifecycle] drives a [Server] bound to a listener. *http.Server satisfies
[Server] as is; other servers need a small adapter. The httpserver and
grpcserver packages are built on it, so a custom server that uses it follows
the same lifecycle and can share their shutdown wait group and signal channel.

# Behavior

  - Start: [Lifecycle.Start] serves in a background goroutine and returns at
    once. It runs once: later calls, and calls after a shutdown, are ignored.
    The shutdown wait group (see [WithShutdownWaitGroup]) is incremented
    before Start returns.
  - Shutdown triggers: a value received on, or the closing of, the shutdown
    signal channel (see [WithShutdownSignalChan]), the cancelation of the
    context passed to Start, or a direct [Lifecycle.Shutdown] call. The first
    two run the shutdown with a context bounded by [WithShutdownTimeout].
  - Shutdown: [Lifecycle.Shutdown] calls Server.Shutdown and returns its
    error. It is safe to call any number of times, concurrently. The wait
    group is decremented exactly once, and only for a started server. The
    listener of a server that was never started is closed, since only Serve
    takes ownership of it. [Lifecycle.Done] is closed once the first call
    has released the wait group or the listener.
  - Serve failures: any error returned by Serve that does not denote a normal
    shutdown (see [WithServerClosedFunc]) is published on
    [Lifecycle.ServeError] and triggers the shutdown, so the application is not
    left waiting on a server that has stopped.

# When To Use

  - A custom network server must run in the background and stop gracefully
    on the service shutdown signal.
  - Several servers must share one shutdown wait group and signal channel,
    for example with github.com/tecnickcom/nurago/pkg/bootstrap.
*/
package srvlifecycle

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
)

var (
	// ErrNilServer is returned by New when the server is nil.
	ErrNilServer = errors.New("server is required")

	// ErrNilListener is returned by New when the listener is nil.
	ErrNilListener = errors.New("listener is required")
)

// Server is a network server driven by a [Lifecycle]. *http.Server satisfies
// it.
type Server interface {
	// Serve serves on l until the server stops. It returns a non-nil error
	// that denotes a normal shutdown (see WithServerClosedFunc) when the
	// server is stopped by Shutdown.
	Serve(l net.Listener) error

	// Shutdown stops the server, waiting for in-flight requests until ctx
	// expires. It may be called more than once.
	Shutdown(ctx context.Context) error
}

// Lifecycle runs a [Server] in the background and shuts it down gracefully.
//
// Create it with [New].
type Lifecycle struct {
	cfg          *config
	srv          Server
	listener     net.Listener
	mu           sync.Mutex
	started      bool
	stopped      bool // set once Shutdown has run, guarding against a late Start.
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	serveErr     chan error // buffered (size 1); receives an abnormal Serve failure
}

// New returns a Lifecycle that serves srv on the bound listener l. The server
// is not started. Call [Lifecycle.Shutdown] to release l if the server is
// never started.
func New(srv Server, l net.Listener, opts ...Option) (*Lifecycle, error) {
	if srv == nil {
		return nil, ErrNilServer
	}

	if l == nil {
		return nil, ErrNilListener
	}

	cfg := defaultConfig()

	for _, applyOpt := range opts {
		if applyOpt == nil {
			return nil, errors.New("option is required")
		}

		err := applyOpt(cfg)
		if err != nil {
			return nil, err
		}
	}

	return &Lifecycle{
		cfg:          cfg,
		srv:          srv,
		listener:     l,
		shutdownDone: make(chan struct{}),
		serveErr:     make(chan error, 1),
	}, nil
}

// Addr returns the address of the listener.
func (l *Lifecycle) Addr() net.Addr {
	return l.listener.Addr()
}

// Started reports whether Start has started the server.
func (l *Lifecycle) Started() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.started
}

// ServeError returns the channel that receives the error that made Serve stop
// unexpectedly. It is buffered (size 1), keeps the first error, and never
// receives nil. A server stopped by Shutdown leaves it empty.
func (l *Lifecycle) ServeError() <-chan error {
	return l.serveErr
}

// Done returns a channel closed when the first Shutdown call has released the
// wait group or the listener.
func (l *Lifecycle) Done() <-chan struct{} {
	return l.shutdownDone
}

// Start serves in a background goroutine and starts a goroutine that runs
// Shutdown when ctx is canceled or the shutdown signal channel fires. It is a
// no-op when the server has already been started or shut down. A nil ctx is
// treated as context.Background().
//
//nolint:contextcheck // a nil ctx is deliberately replaced with context.Background()
func (l *Lifecycle) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	// Register the running server with the wait group synchronously, before any
	// goroutine (including the shutdown one) can decrement it. The matching
	// decrement happens exactly once in Shutdown via sync.Once, and only for a
	// started server.
	l.mu.Lock()

	if l.started || l.stopped {
		l.mu.Unlock()
		l.cfg.logger.Warn("start ignored: server already started or shut down")

		return
	}

	l.started = true
	l.cfg.shutdownWaitGroup.Add(1)
	l.mu.Unlock()

	// wait for shutdown signal, direct Shutdown call, or context cancelation
	go func() { //nolint:gosec
		select {
		case <-l.cfg.shutdownSignalChan:
			l.cfg.logger.Debug("shutdown notification received")
		case <-l.shutdownDone:
			// Shutdown was already called directly: exit without leaking this goroutine.
			return
		case <-ctx.Done():
			l.cfg.logger.Warn("context canceled")
		}

		// The shutdown context is independent from the parent context.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), l.cfg.shutdownTimeout)
		defer cancel()

		_ = l.Shutdown(shutdownCtx) //nolint:contextcheck
	}()

	go l.serve() //nolint:gosec,contextcheck // the shutdown after a serve failure does not depend on ctx

	l.cfg.logger.Info("listening for " + l.cfg.name + " requests")
}

// Shutdown calls Server.Shutdown and returns its error. The first call also
// prevents a later Start, either decrements the wait group (started server) or
// closes the listener (never-started server), and then closes the Done channel
// and releases the shutdown-monitor goroutine.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	l.cfg.logger.Debug("shutting down " + l.cfg.name + " server")

	err := l.srv.Shutdown(ctx)

	l.shutdownOnce.Do(func() {
		l.release()

		// Unblock the internal shutdown-monitor goroutine (if any),
		// so a direct Shutdown call does not leak it.
		close(l.shutdownDone)
	})

	if err != nil {
		l.cfg.logger.With(slog.Any("error", err)).Warn(l.cfg.name + " server shutdown completed with error")

		return err //nolint:wrapcheck // the server error is returned as-is
	}

	l.cfg.logger.Debug(l.cfg.name + " server shutdown complete")

	return nil
}

// release prevents a later Start and either decrements the wait group
// (started server) or closes the listener (never-started server).
func (l *Lifecycle) release() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stopped = true

	if l.started {
		l.cfg.shutdownWaitGroup.Add(-1)
		return
	}

	// Servers close only the listeners passed to Serve, so a never-started
	// server must release its bound listener explicitly.
	cerr := l.listener.Close()
	if cerr != nil {
		l.cfg.logger.With(slog.Any("error", cerr)).Debug("failed closing the " + l.cfg.name + " server listener")
	}
}

// serve runs Serve. On any failure other than a normal shutdown it publishes
// the error on the ServeError channel and runs Shutdown, so the wait group is
// released and the application is not left waiting on a dead server.
func (l *Lifecycle) serve() {
	err := l.srv.Serve(l.listener)
	if err == nil || l.cfg.serverClosedFn(err) {
		l.cfg.logger.Debug("closed " + l.cfg.name + " server")
		return
	}

	l.cfg.logger.With(slog.Any("error", err)).Error("unexpected " + l.cfg.name + " server failure")

	// Non-blocking publish of the failure (buffer size 1, never overwritten).
	select {
	case l.serveErr <- err:
	default:
	}

	// Ensure the wait group is released and the monitor goroutine unblocked.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), l.cfg.shutdownTimeout)
	defer cancel()

	_ = l.Shutdown(shutdownCtx)
}
