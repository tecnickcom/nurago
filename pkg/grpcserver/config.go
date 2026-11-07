package grpcserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
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
	instrumenters        []grpcutil.ServerInstrumenter
	extraServerOptions   []grpc.ServerOption
	logger               *slog.Logger
	lifecycleOpts        []srvlifecycle.Option
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
		redactFn:             redact.Default().BytesToString,
		maxDumpSize:          defaultMaxDumpSize,
		disabledMethodLogger: make(map[string]bool),
		panicHandlerFunc:     defaultPanicHandler,
		logger:               slog.Default(),
		rnd:                  random.New(nil),
	}
}

// newConfig returns the default configuration with opts applied. It also
// validates the shutdown options, so they are rejected before the listener is
// bound.
func newConfig(opts []Option) (*config, error) {
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

	err := srvlifecycle.ValidateOptions(cfg.lifecycleOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed creating the grpc server lifecycle: %w", err)
	}

	return cfg, nil
}

// defaultPanicHandler answers a recovered panic with codes.Internal.
func defaultPanicHandler(_ context.Context, _ any) error {
	return status.Error(codes.Internal, codes.Internal.String()) //nolint:wrapcheck // gRPC status errors are returned as-is
}

// serverOptions returns the grpc.ServerOption list: the default interceptor
// chain with the instrumenter options between the request and the recovery
// interceptors, preceded by a recovery interceptor of their own when they add
// interceptors, transport settings, and any options supplied with
// WithServerOption.
func (c *config) serverOptions() []grpc.ServerOption {
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(c.unaryRequestInterceptor),
		grpc.ChainStreamInterceptor(c.streamRequestInterceptor),
	}

	if c.hasInstrumenterInterceptors() {
		opts = append(opts,
			grpc.ChainUnaryInterceptor(c.unaryRecoveryInterceptor),
			grpc.ChainStreamInterceptor(c.streamRecoveryInterceptor),
		)
	}

	for _, in := range c.instrumenters {
		opts = append(opts, in.ServerOptions()...)
	}

	unary := append([]grpc.UnaryServerInterceptor{
		c.unaryRecoveryInterceptor,
		c.unaryDeadlineInterceptor,
	}, c.unaryInterceptors...)

	stream := append([]grpc.StreamServerInterceptor{
		c.streamRecoveryInterceptor,
	}, c.streamInterceptors...)

	opts = append(opts,
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)

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

// lifecycleOptions returns the srvlifecycle options of the server: the gRPC
// defaults followed by the shutdown options.
func (c *config) lifecycleOptions() []srvlifecycle.Option {
	return append([]srvlifecycle.Option{
		srvlifecycle.WithName("grpc"),
		srvlifecycle.WithLogger(c.logger),
		srvlifecycle.WithServerClosedFunc(func(err error) bool { return errors.Is(err, grpc.ErrServerStopped) }),
	}, c.lifecycleOpts...)
}

// hasInstrumenterInterceptors reports whether any instrumenter adds
// interceptors. An instrumenter that does not implement
// grpcutil.ServerInterceptorReporter is assumed to add them.
func (c *config) hasInstrumenterInterceptors() bool {
	for _, in := range c.instrumenters {
		r, ok := in.(grpcutil.ServerInterceptorReporter)
		if !ok || r.HasServerInterceptors() {
			return true
		}
	}

	return false
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

// validateFullMethod checks that name has the "/package.Service/Method" form.
func validateFullMethod(name string) error {
	if _, _, ok := grpcutil.ParseFullMethod(name); !ok {
		return fmt.Errorf("%w: %q", ErrInvalidFullMethod, name)
	}

	return nil
}
