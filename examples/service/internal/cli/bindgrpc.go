package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/nuragoexampleowner/nuragoexample/internal/grpchandler"
	instr "github.com/nuragoexampleowner/nuragoexample/internal/metrics"
	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/healthcheck"
	"github.com/tecnickcom/nurago/pkg/redact"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// startGRPCServer builds, starts, and registers the gRPC server.
//
// The server shares the logger, redactor, wait group, and shutdown signal of
// the HTTP servers. It exposes the services registered by binder and the
// grpc.health.v1.Health service, backed by the same health checks as the HTTP
// status route. Health Check and Watch calls are not logged, since load
// balancers poll them. The Prometheus gRPC metrics are exported by the
// monitoring server.
func startGRPCServer(
	ctx context.Context,
	srv cfgServerGRPC,
	binder grpcserver.Binder,
	healthchecks []healthcheck.HealthCheck,
	l *slog.Logger,
	mtr instr.Metrics,
	logRedactor *redact.Redactor,
	wg *sync.WaitGroup,
	sc chan struct{},
) error {
	instrumenter, err := mtr.GRPCServerInstrumenter()
	if err != nil {
		return fmt.Errorf("failed to instrument the gRPC server: %w", err)
	}

	timeout := time.Duration(srv.Timeout) * time.Second

	opts := []grpcserver.Option{
		grpcserver.WithLogger(l),
		grpcserver.WithServerAddr(srv.Address),
		grpcserver.WithRequestTimeout(timeout),
		grpcserver.WithMaxRecvMsgSize(grpchandler.MaxRecvMsgSize),
		grpcserver.WithHealthService(healthchecks, grpcserver.WithHealthCheckTimeout(timeout)),
		grpcserver.WithoutMethodLogger(healthpb.Health_Check_FullMethodName, healthpb.Health_Watch_FullMethodName),
		grpcserver.WithInstrumenter(instrumenter),
		grpcserver.WithRedactFn(logRedactor.BytesToString),
		grpcserver.WithShutdownWaitGroup(wg),
		grpcserver.WithShutdownSignalChan(sc),
	}

	// Reflection lets any client list the services: enable it only on
	// listeners that are not reachable from untrusted networks.
	if srv.Reflection {
		opts = append(opts, grpcserver.WithReflection())
	}

	if srv.TLSCertFile != "" {
		tlsOpt, err := grpcTLSOption(srv.TLSCertFile, srv.TLSKeyFile)
		if err != nil {
			return err
		}

		opts = append(opts, tlsOpt)
	}

	server, err := grpcserver.New(ctx, binder, opts...)
	if err != nil {
		return fmt.Errorf("error creating gRPC server: %w", err)
	}

	server.StartServer()

	return nil
}

// grpcTLSOption reads the PEM-encoded certificate and key files of the gRPC
// server.
func grpcTLSOption(certFile, keyFile string) (grpcserver.Option, error) {
	cert, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the gRPC TLS certificate: %w", err)
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the gRPC TLS key: %w", err)
	}

	return grpcserver.WithTLSCertData(cert, key), nil
}
