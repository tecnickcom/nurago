package grpcserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Option configures a [GRPCServer] instance.
type Option func(*config) error

// WithServerAddr sets the address the server will bind to.
func WithServerAddr(addr string) Option {
	return func(cfg *config) error {
		if !srvlifecycle.IsValidAddr(addr) {
			return fmt.Errorf("invalid grpc server address: %s", addr)
		}

		cfg.serverAddr = addr

		return nil
	}
}

// WithRequestTimeout sets the deadline applied to unary RPCs that arrive
// without one. A deadline sent by the client is never extended or shortened.
// Streaming RPCs are not affected (see WithMethodTimeout).
//
// The deadline is set on the RPC context: handlers must honor its
// cancelation, as with any gRPC deadline.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid requestTimeout")
		}

		cfg.requestTimeout = timeout

		return nil
	}
}

// WithMethodTimeout sets the deadline applied to fullMethod
// ("/package.Service/Method") when the RPC arrives without one. It overrides
// WithRequestTimeout for unary RPCs and is the only way to set a default
// deadline for a streaming RPC. A negative timeout disables the default
// deadline for the method.
//
// The deadline is set with grpc.InTapHandle, so it also ends blocked stream
// reads and writes, and it cannot be combined with a grpc.InTapHandle passed
// to WithServerOption. Unlike a client deadline, it does not reset the stream:
// handlers must honor the cancelation of the RPC context.
func WithMethodTimeout(fullMethod string, timeout time.Duration) Option {
	return func(cfg *config) error {
		err := validateFullMethod(fullMethod)
		if err != nil {
			return err
		}

		if timeout == 0 {
			return errors.New("invalid methodTimeout")
		}

		cfg.methodTimeouts[fullMethod] = timeout

		return nil
	}
}

// WithMaxRecvMsgSize sets the maximum size in bytes of a message the server
// can receive. When not set, the grpc-go default (4 MiB) applies. Larger
// messages are rejected with codes.ResourceExhausted.
func WithMaxRecvMsgSize(n int) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid maxRecvMsgSize")
		}

		cfg.maxRecvMsgSize = n

		return nil
	}
}

// WithMaxSendMsgSize sets the maximum size in bytes of a message the server
// can send. When not set, the grpc-go default (math.MaxInt32) applies.
func WithMaxSendMsgSize(n int) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid maxSendMsgSize")
		}

		cfg.maxSendMsgSize = n

		return nil
	}
}

// WithMaxHeaderListSize sets the maximum size in bytes of the header list the
// server accepts. When not set, the grpc-go default (16 MiB) applies.
// Lowering it is a standard hardening measure for public listeners.
func WithMaxHeaderListSize(n uint32) Option {
	return func(cfg *config) error {
		if n == 0 {
			return errors.New("invalid maxHeaderListSize")
		}

		cfg.maxHeaderListSize = n

		return nil
	}
}

// WithKeepaliveParams sets the server keepalive and connection age
// parameters. When not set, the grpc-go defaults apply.
func WithKeepaliveParams(kp keepalive.ServerParameters) Option {
	return func(cfg *config) error {
		cfg.keepaliveParams = &kp
		return nil
	}
}

// WithKeepaliveEnforcementPolicy sets the policy for client keepalive pings.
// When not set, the grpc-go default applies: pings more frequent than every 5
// minutes, or pings without active streams, cause the connection to be closed.
func WithKeepaliveEnforcementPolicy(kep keepalive.EnforcementPolicy) Option {
	return func(cfg *config) error {
		cfg.keepalivePolicy = &kep
		return nil
	}
}

// WithShutdownTimeout sets the time Shutdown waits for in-flight RPCs when it
// is triggered by the context or the shutdown signal channel. New fails when
// timeout is not positive.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownTimeout(timeout))

		return nil
	}
}

// WithShutdownWaitGroup sets the shared waiting group to communicate externally when the server is shutdown.
// The counter is incremented when the server starts (StartServer/StartServerCtx)
// and decremented exactly once on shutdown, so Wait returns immediately if
// called before the server has been started. New fails when wg is nil.
func WithShutdownWaitGroup(wg *sync.WaitGroup) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownWaitGroup(wg))

		return nil
	}
}

// WithShutdownSignalChan sets the shared channel used to signal a shutdown.
// When a value is received on the channel the server initiates the shutdown
// process. Close the channel (rather than sending a single value) to wake every
// server that shares it, since a single send is delivered to only one receiver.
// New fails when ch is nil.
func WithShutdownSignalChan(ch chan struct{}) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownSignalChan(ch))

		return nil
	}
}

// WithTLSCertData enables TLS with the given PEM certificate and key data,
// with TLS 1.2 as the minimum version.
func WithTLSCertData(pemCert, pemKey []byte) Option {
	return func(cfg *config) error {
		cert, err := tls.X509KeyPair(pemCert, pemKey)
		if err != nil {
			return fmt.Errorf("failed configuring TLS: %w", err)
		}

		cfg.tlsConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}

		return nil
	}
}

// WithTLSConfig sets a custom TLS configuration, giving full control over
// certificates, client authentication, and cipher suites. When both this and
// WithTLSCertData are supplied, the last option wins. The configuration is
// cloned by the gRPC transport credentials, which add "h2" to NextProtos and
// enforce TLS 1.2 as the minimum version when none is set.
func WithTLSConfig(tlsConfig *tls.Config) Option {
	return func(cfg *config) error {
		if tlsConfig == nil {
			return errors.New("tlsConfig is required")
		}

		cfg.tlsConfig = tlsConfig

		return nil
	}
}

// WithReflection registers the gRPC server reflection service (v1 and
// v1alpha), which lets clients such as grpcurl discover the registered
// services. See the package Security section before enabling it.
func WithReflection() Option {
	return func(cfg *config) error {
		cfg.reflection = true
		return nil
	}
}

// WithHealthService registers the standard grpc.health.v1.Health service
// (Check, Watch, List) backed by checks, which run as in healthrun.Runner.
// healthcheck.HealthCheck is the same type, so the checks of an HTTP health
// endpoint can be reused. The service name "" reports SERVING only when every
// check passes; each non-empty check ID is also a service name that reports
// the checks registered under it. When the server starts shutting down, every
// service reports NOT_SERVING and open Watch streams end. The checks slice is
// copied. At most 99 distinct non-empty check IDs are accepted, so that List
// reports at most 100 services. A Binder that registers its own health service
// makes New return ErrServiceRegistration.
func WithHealthService(checks []healthrun.HealthCheck, opts ...HealthOption) Option {
	return func(cfg *config) error {
		if healthServiceCount(checks) > maxHealthListServices {
			return fmt.Errorf("too many health check IDs: the maximum is %d", maxHealthListServices-1)
		}

		hc := &healthConfig{
			checks:        checks,
			watchInterval: defaultHealthWatchInterval,
		}

		for _, applyOpt := range opts {
			if applyOpt == nil {
				return errors.New("healthOption is required")
			}

			err := applyOpt(hc)
			if err != nil {
				return err
			}
		}

		cfg.health = hc

		return nil
	}
}

// WithTraceIDMetadataKey overrides the default metadata key used to read and
// return the trace ID (grpcutil.DefaultTraceIDMetadataKey). The key is
// lowercased and must pass grpcutil.ValidTraceIDMetadataKey.
func WithTraceIDMetadataKey(key string) Option {
	return func(cfg *config) error {
		if !grpcutil.ValidTraceIDMetadataKey(key) {
			return fmt.Errorf("%w: %q", ErrInvalidMetadataKey, key)
		}

		cfg.traceIDMetadataKey = strings.ToLower(key)

		return nil
	}
}

// WithContextValues makes RPC contexts expose the values stored under keys in
// the context passed to New. The values are read once, when New runs; keys
// without a value are ignored. A value found in the RPC context takes
// precedence. Keys must be non-nil and comparable, as for context.WithValue.
func WithContextValues(keys ...any) Option {
	return func(cfg *config) error {
		for _, k := range keys {
			if k == nil || !reflect.TypeOf(k).Comparable() {
				return errors.New("invalid context key")
			}
		}

		cfg.contextKeys = append(cfg.contextKeys, keys...)

		return nil
	}
}

// WithRedactFn sets the function used to redact debug-level message dumps and
// error messages in the logs.
func WithRedactFn(fn RedactFn) Option {
	return func(cfg *config) error {
		if fn == nil {
			return errors.New("redactFn is required")
		}

		cfg.redactFn = fn

		return nil
	}
}

// WithMaxDumpSize sets the maximum encoded size in bytes of a unary message
// dumped into a debug-level log entry (default 1 MiB). Larger messages are
// logged as omitted.
func WithMaxDumpSize(n int) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid maxDumpSize")
		}

		cfg.maxDumpSize = n

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

// WithoutRequestLogger disables the per-RPC log entry for all methods.
// Trace ID injection and panic logging are not affected.
func WithoutRequestLogger() Option {
	return func(cfg *config) error {
		cfg.disableRequestLogger = true
		return nil
	}
}

// WithoutMethodLogger disables the per-RPC log entry for the given methods
// ("/package.Service/Method"), for example health checks polled by a load
// balancer. Trace ID injection and panic logging are not affected.
func WithoutMethodLogger(fullMethods ...string) Option {
	return func(cfg *config) error {
		for _, m := range fullMethods {
			err := validateFullMethod(m)
			if err != nil {
				return err
			}
		}

		for _, m := range fullMethods {
			cfg.disabledMethodLogger[m] = true
		}

		return nil
	}
}

// WithPanicHandlerFunc sets the function that builds the error returned to
// the client when an RPC handler panics. The panic is logged before the
// function is called.
func WithPanicHandlerFunc(fn PanicHandlerFunc) Option {
	return func(cfg *config) error {
		if fn == nil {
			return errors.New("panicHandlerFunc is required")
		}

		cfg.panicHandlerFunc = fn

		return nil
	}
}

// WithUnaryInterceptor adds unary interceptors after the default ones, in the
// provided order. Nil interceptors are rejected.
func WithUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) Option {
	return func(cfg *config) error {
		for _, i := range interceptors {
			if i == nil {
				return errors.New("unaryInterceptor is required")
			}
		}

		cfg.unaryInterceptors = append(cfg.unaryInterceptors, interceptors...)

		return nil
	}
}

// WithStreamInterceptor adds stream interceptors after the default ones, in
// the provided order. Nil interceptors are rejected.
func WithStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) Option {
	return func(cfg *config) error {
		for _, i := range interceptors {
			if i == nil {
				return errors.New("streamInterceptor is required")
			}
		}

		cfg.streamInterceptors = append(cfg.streamInterceptors, interceptors...)

		return nil
	}
}

// WithInstrumenter adds the server options of instrumenter, for example a
// metrics or tracing backend. Interceptors added with grpc.ChainUnaryInterceptor
// or grpc.ChainStreamInterceptor run after the request inject and logger
// interceptor and before panic recovery, so they see the trace ID and the
// codes.Internal status of a recovered panic. Its other options follow the
// same rules as WithServerOption. When instrumenter also implements
// grpcutil.ServerInitializer, New calls InitializeServer once every service
// is registered. Instrumenters are applied in the order given. A nil
// instrumenter is rejected.
func WithInstrumenter(instrumenter grpcutil.ServerInstrumenter) Option {
	return func(cfg *config) error {
		if instrumenter == nil {
			return errors.New("instrumenter is required")
		}

		cfg.instrumenters = append(cfg.instrumenters, instrumenter)

		return nil
	}
}

// WithServerOption adds raw grpc.ServerOption values, applied after the ones
// built by this package. Use it for settings without a dedicated option, such
// as grpc.StatsHandler. A grpc.Creds option replaces the TLS configuration,
// and interceptors set with grpc.UnaryInterceptor or grpc.StreamInterceptor
// run before the default interceptor chain; use WithUnaryInterceptor and
// WithStreamInterceptor instead. A grpc.InTapHandle conflicts with a positive
// WithMethodTimeout and makes New return ErrInvalidServerOption. Nil options
// are rejected.
func WithServerOption(opts ...grpc.ServerOption) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("serverOption is required")
			}
		}

		cfg.extraServerOptions = append(cfg.extraServerOptions, opts...)

		return nil
	}
}
