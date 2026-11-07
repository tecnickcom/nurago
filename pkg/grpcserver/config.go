package grpcserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// defaultMaxDumpSize is the default cap, in encoded message bytes, on unary
// messages dumped into debug-level log entries.
const defaultMaxDumpSize = 1 << 20 // 1 MiB

// RedactFn redacts sensitive values from logged byte payloads and returns a redacted string.
type RedactFn = grpcutil.RedactFn

// PanicHandlerFunc returns the error sent to the client when an RPC handler
// panics. p is the recovered value. A nil return is replaced with the default
// codes.Internal error.
type PanicHandlerFunc func(ctx context.Context, p any) error

// config contains the configuration for the gRPC server.
type config struct {
	serverAddr           string
	traceIDMetadataKey   string
	requestTimeout       time.Duration
	methodTimeouts       map[string]time.Duration
	maxRecvMsgSize       int
	maxSendMsgSize       int
	maxHeaderListSize    uint32
	keepaliveParams      *keepalive.ServerParameters
	keepalivePolicy      *keepalive.EnforcementPolicy
	shutdownTimeout      time.Duration
	tlsConfig            *tls.Config
	reflection           bool
	health               *healthConfig
	redactFn             RedactFn
	maxDumpSize          int
	disableRequestLogger bool
	disabledMethodLogger map[string]bool
	panicHandlerFunc     PanicHandlerFunc
	unaryInterceptors    []grpc.UnaryServerInterceptor
	streamInterceptors   []grpc.StreamServerInterceptor
	extraServerOptions   []grpc.ServerOption
	logger               *slog.Logger
	shutdownWaitGroup    *sync.WaitGroup
	shutdownSignalChan   chan struct{}
	rnd                  *random.Rnd
	contextKeys          []any
	baseValues           []ctxValue
}

// ctxValue is a value of the New context exposed by RPC contexts.
type ctxValue struct {
	key any
	val any
}

// defaultConfig returns the default configuration for the gRPC server.
func defaultConfig() *config {
	return &config{
		serverAddr:           ":8019",
		traceIDMetadataKey:   grpcutil.DefaultTraceIDMetadataKey,
		methodTimeouts:       make(map[string]time.Duration),
		shutdownTimeout:      30 * time.Second,
		redactFn:             redact.Default().BytesToString,
		maxDumpSize:          defaultMaxDumpSize,
		disabledMethodLogger: make(map[string]bool),
		panicHandlerFunc:     defaultPanicHandler,
		logger:               slog.Default(),
		shutdownWaitGroup:    &sync.WaitGroup{},
		shutdownSignalChan:   make(chan struct{}),
		rnd:                  random.New(nil),
	}
}

// defaultPanicHandler answers a recovered panic with codes.Internal.
func defaultPanicHandler(_ context.Context, _ any) error {
	return status.Error(codes.Internal, codes.Internal.String()) //nolint:wrapcheck // gRPC status errors are returned as-is
}

// serverOptions returns the grpc.ServerOption list: the default interceptor
// chain, transport settings, and any options supplied with WithServerOption.
func (c *config) serverOptions() []grpc.ServerOption {
	unary := append([]grpc.UnaryServerInterceptor{
		c.unaryRequestInterceptor,
		c.unaryRecoveryInterceptor,
		c.unaryDeadlineInterceptor,
	}, c.unaryInterceptors...)

	stream := append([]grpc.StreamServerInterceptor{
		c.streamRequestInterceptor,
		c.streamRecoveryInterceptor,
	}, c.streamInterceptors...)

	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	}

	if c.hasMethodDeadline() {
		opts = append(opts, grpc.InTapHandle(c.methodDeadlineTap))
	}

	if c.tlsConfig != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(c.tlsConfig)))
	}

	if c.maxRecvMsgSize > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(c.maxRecvMsgSize))
	}

	if c.maxSendMsgSize > 0 {
		opts = append(opts, grpc.MaxSendMsgSize(c.maxSendMsgSize))
	}

	if c.maxHeaderListSize > 0 {
		opts = append(opts, grpc.MaxHeaderListSize(c.maxHeaderListSize))
	}

	if c.keepaliveParams != nil {
		opts = append(opts, grpc.KeepaliveParams(*c.keepaliveParams))
	}

	if c.keepalivePolicy != nil {
		opts = append(opts, grpc.KeepaliveEnforcementPolicy(*c.keepalivePolicy))
	}

	return append(opts, c.extraServerOptions...)
}

// methodTimeout returns the per-method timeout for fullMethod, or def when
// none is set. A negative value means the timeout is disabled.
func (c *config) methodTimeout(fullMethod string, def time.Duration) time.Duration {
	if t, ok := c.methodTimeouts[fullMethod]; ok {
		return t
	}

	return def
}

// hasMethodDeadline reports whether any per-method timeout sets a deadline.
func (c *config) hasMethodDeadline() bool {
	for _, t := range c.methodTimeouts {
		if t > 0 {
			return true
		}
	}

	return false
}

// isLoggerEnabled reports whether the request logger runs for fullMethod.
func (c *config) isLoggerEnabled(fullMethod string) bool {
	return !c.disableRequestLogger && !c.disabledMethodLogger[fullMethod]
}

// validateAddr checks if a gRPC server bind address is valid.
// The host part may be empty, a hostname, an IPv4 or a bracketed IPv6 address
// (e.g. ":8019", "localhost:8019", "0.0.0.0:8019", "[::1]:8019").
func validateAddr(addr string) error {
	addrErr := fmt.Errorf("invalid grpc server address: %s", addr)

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addrErr
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		return addrErr
	}

	// Port 0 is valid: it asks the operating system for an ephemeral port.
	if portInt < 0 || portInt > math.MaxUint16 {
		return addrErr
	}

	return nil
}

// validateFullMethod checks that name has the "/package.Service/Method" form.
func validateFullMethod(name string) error {
	if _, _, ok := grpcutil.ParseFullMethod(name); !ok {
		return fmt.Errorf("%w: %q", ErrInvalidFullMethod, name)
	}

	return nil
}
