package healthrun

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithLogger(t *testing.T) {
	t.Parallel()

	r := &Runner{}

	v := slog.Default()
	WithLogger(v)(r)
	require.Equal(t, v, r.logger)
}

func TestWithTimeout(t *testing.T) {
	t.Parallel()

	r := &Runner{}

	WithTimeout(time.Second)(r)
	require.Equal(t, time.Second, r.timeout)
}

func TestWithoutCheckWarnings(t *testing.T) {
	t.Parallel()

	r := &Runner{}

	WithoutCheckWarnings()(r)
	require.True(t, r.noCheckWarnings)
}
