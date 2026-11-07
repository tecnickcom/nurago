package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nuragoexampleowner/nuragoexample/internal/metrics"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/healthcheck"
)

// mockMetricsGRPCError is a working metrics client whose gRPC instrumenter
// cannot be created.
type mockMetricsGRPCError struct {
	*metrics.Client
}

func (c *mockMetricsGRPCError) GRPCServerInstrumenter() (grpcutil.ServerInstrumenter, error) {
	return nil, errors.New("TEST ERROR")
}

// writeTestTLSFiles writes a self-signed PEM certificate and its key to a
// temporary directory and returns the file paths.
func writeTestTLSFiles(t *testing.T) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	return certFile, keyFile
}

// boundTestAddr returns the address of a listener that stays open until the
// test ends.
func boundTestAddr(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	return l.Addr().String()
}

// initializedTestMetrics returns a metrics client with the underlying client
// created.
func initializedTestMetrics(t *testing.T) metrics.Metrics {
	t.Helper()

	m := metrics.New()

	_, err := m.CreateMetricsClientFunc()
	require.NoError(t, err)

	return m
}

//nolint:gocognit
func Test_startGRPCServer(t *testing.T) {
	t.Parallel()

	certFile, keyFile := writeTestTLSFiles(t)

	tests := []struct {
		name        string
		fcfg        func(cfg cfgServerGRPC) cfgServerGRPC
		mtr         func() metrics.Metrics
		preBindAddr bool
		wantErr     bool
	}{
		{
			name:    "succeed with plaintext",
			wantErr: false,
		},
		{
			name: "succeed with TLS and reflection",
			fcfg: func(cfg cfgServerGRPC) cfgServerGRPC {
				cfg.Reflection = true
				cfg.TLSCertFile = certFile
				cfg.TLSKeyFile = keyFile

				return cfg
			},
			wantErr: false,
		},
		{
			name:        "fails with the port already bound",
			preBindAddr: true,
			wantErr:     true,
		},
		{
			name:    "fails without an initialized metrics client",
			mtr:     func() metrics.Metrics { return metrics.New() },
			wantErr: true,
		},
		{
			name:    "fails to instrument the server",
			mtr:     func() metrics.Metrics { return &mockMetricsGRPCError{Client: metrics.New()} },
			wantErr: true,
		},
		{
			name: "fails with a missing TLS certificate file",
			fcfg: func(cfg cfgServerGRPC) cfgServerGRPC {
				cfg.TLSCertFile = "/missing/cert.pem"
				cfg.TLSKeyFile = keyFile

				return cfg
			},
			wantErr: true,
		},
		{
			name: "fails with a missing TLS key file",
			fcfg: func(cfg cfgServerGRPC) cfgServerGRPC {
				cfg.TLSCertFile = certFile
				cfg.TLSKeyFile = "/missing/key.pem"

				return cfg
			},
			wantErr: true,
		},
		{
			name: "fails with an invalid TLS key pair",
			fcfg: func(cfg cfgServerGRPC) cfgServerGRPC {
				cfg.TLSCertFile = certFile
				cfg.TLSKeyFile = certFile

				return cfg
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := cfgServerGRPC{
				Enabled: true,
				Address: "127.0.0.1:0",
				Timeout: 1,
			}

			if tt.fcfg != nil {
				cfg = tt.fcfg(cfg)
			}

			if tt.preBindAddr {
				cfg.Address = boundTestAddr(t)
			}

			mtr := initializedTestMetrics(t)

			if tt.mtr != nil {
				mtr = tt.mtr()
			}

			wg := &sync.WaitGroup{}
			sc := make(chan struct{})

			err := startGRPCServer(
				t.Context(), cfg, grpcserver.NopBinder(), []healthcheck.HealthCheck{},
				slog.New(slog.DiscardHandler), mtr, newLogRedactor(), wg, sc,
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			close(sc)
			wg.Wait()
		})
	}
}
