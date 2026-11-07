package srvlifecycle

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithName(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithName("")(cfg))
	require.NoError(t, WithName("tcp")(cfg))
	require.Equal(t, "tcp", cfg.name)
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	logger := slog.New(slog.DiscardHandler)

	require.Error(t, WithLogger(nil)(cfg))
	require.NoError(t, WithLogger(logger)(cfg))
	require.Same(t, logger, cfg.logger)
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithShutdownTimeout(0)(cfg))
	require.NoError(t, WithShutdownTimeout(time.Second)(cfg))
	require.Equal(t, time.Second, cfg.shutdownTimeout)
}

func TestWithShutdownWaitGroup(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	wg := &sync.WaitGroup{}

	require.Error(t, WithShutdownWaitGroup(nil)(cfg))
	require.NoError(t, WithShutdownWaitGroup(wg)(cfg))
	require.Same(t, wg, cfg.shutdownWaitGroup)
}

func TestWithShutdownSignalChan(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	ch := make(chan struct{})

	require.Error(t, WithShutdownSignalChan(nil)(cfg))
	require.NoError(t, WithShutdownSignalChan(ch)(cfg))
	require.Equal(t, (<-chan struct{})(ch), cfg.shutdownSignalChan)
}

func TestWithServerClosedFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithServerClosedFunc(nil)(cfg))
	require.NoError(t, WithServerClosedFunc(func(error) bool { return true })(cfg))
	require.True(t, cfg.serverClosedFn(errClosed))
}
