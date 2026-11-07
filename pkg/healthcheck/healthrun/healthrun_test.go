package healthrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type testChecker struct {
	err error
}

func (c *testChecker) HealthCheck(_ context.Context) error {
	return c.err
}

type panicChecker struct {
	msg string
}

func (c *panicChecker) HealthCheck(_ context.Context) error {
	panic(c.msg)
}

// releaseChecker blocks until released.
type releaseChecker struct {
	release <-chan struct{}
}

func (c *releaseChecker) HealthCheck(_ context.Context) error {
	<-c.release

	return nil
}

func TestNew(t *testing.T) {
	t.Parallel()

	c := &testChecker{}
	hc := New("hc-id_1", c)
	require.Equal(t, "hc-id_1", hc.ID)
	require.Equal(t, c, hc.Checker)
}

func TestHealthCheckFunc(t *testing.T) {
	t.Parallel()

	called := false

	var ok HealthChecker = HealthCheckFunc(func(_ context.Context) error {
		called = true

		return nil
	})

	require.NoError(t, ok.HealthCheck(t.Context()))
	require.True(t, called)

	sentinel := errors.New("boom")
	failing := HealthCheckFunc(func(_ context.Context) error { return sentinel })
	require.ErrorIs(t, failing.HealthCheck(t.Context()), sentinel)
}
