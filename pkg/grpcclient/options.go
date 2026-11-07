package grpcclient

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/stats"
)

// Option configures the client connection built by [New].
type Option func(*config) error

// WithTimeout sets the deadline applied to unary RPCs whose context has none
// (default 1 minute). A deadline already set on the context is never extended
// or shortened. A zero or negative timeout disables the default deadline.
// Streaming RPCs are not affected.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		cfg.timeout = timeout
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

// WithComponent sets the component name logged in every entry (default "-").
func WithComponent(name string) Option {
	return func(cfg *config) error {
		if name == "" {
			return errors.New("component is required")
		}

		cfg.component = name

		return nil
	}
}

// WithLogPrefix sets a prefix applied to every log field name (for example
// "grpc_client_"). The log message is not prefixed. The default is no prefix.
func WithLogPrefix(prefix string) Option {
	return func(cfg *config) error {
		cfg.logPrefix = prefix
		return nil
	}
}

// WithTraceIDMetadataKey overrides the default metadata key used to send the
// trace ID (grpcutil.DefaultTraceIDMetadataKey). The key is lowercased and
// must pass grpcutil.ValidTraceIDMetadataKey.
func WithTraceIDMetadataKey(key string) Option {
	return func(cfg *config) error {
		if !grpcutil.ValidTraceIDMetadataKey(key) {
			return fmt.Errorf("%w: %q", ErrInvalidMetadataKey, key)
		}

		cfg.traceIDMetadataKey = strings.ToLower(key)

		return nil
	}
}

// WithRedactFn sets the function used to redact debug-level message dumps and
// logged error messages. It is called concurrently by simultaneous RPCs.
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

// WithTLSConfig sets a custom TLS configuration, for example to trust a
// private CA or to send a client certificate. The configuration is cloned by
// the gRPC transport credentials, which add "h2" to NextProtos and enforce TLS
// 1.2 as the minimum version when none is set. It cannot be combined with
// WithInsecure or WithCredentialsBundle.
func WithTLSConfig(tlsConfig *tls.Config) Option {
	return func(cfg *config) error {
		if tlsConfig == nil {
			return errors.New("tlsConfig is required")
		}

		cfg.tlsConfig = tlsConfig

		return nil
	}
}

// WithInsecure disables transport security, so RPCs are sent in plaintext.
// Use it only for local development or behind a trusted network boundary. It
// cannot be combined with WithTLSConfig or WithCredentialsBundle, and grpc-go
// rejects per-RPC credentials that require transport security.
func WithInsecure() Option {
	return func(cfg *config) error {
		cfg.insecure = true
		return nil
	}
}

// WithCredentialsBundle sets a credentials bundle (for example Google default,
// ALTS, or xDS credentials) that supplies both the transport and the per-RPC
// credentials, in place of the default TLS credentials. It cannot be combined
// with WithInsecure or WithTLSConfig.
func WithCredentialsBundle(bundle credentials.Bundle) Option {
	return func(cfg *config) error {
		if bundle == nil {
			return errors.New("credentialsBundle is required")
		}

		cfg.credsBundle = bundle

		return nil
	}
}

// WithRetryPolicy adds a retry policy, executed by grpc-go. Call it once per
// scope: a policy whose scope overlaps one already set returns
// ErrInvalidRetryPolicy, as does a policy that fails validation. The policy
// slices are copied.
//
// The policies are sent as the default service config, which a service config
// returned by the name resolver replaces, and which a
// grpc.WithDefaultServiceConfig passed to WithDialOption overrides. New returns
// ErrInvalidRetryPolicy when a MaxAttempts exceeds the call attempt cap (5,
// see WithMaxCallAttempts).
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(cfg *config) error {
		return cfg.addRetryPolicy(policy)
	}
}

// WithMaxCallAttempts sets the cap on the attempts of a call, including the
// original one (grpc-go default 5). It must be at least 2. The cap applies to
// every retry and hedging policy of the channel, including those of a service
// config returned by the name resolver.
func WithMaxCallAttempts(n int) Option {
	return func(cfg *config) error {
		if n < 2 {
			return errors.New("invalid maxCallAttempts")
		}

		cfg.maxCallAttempts = n

		return nil
	}
}

// WithKeepaliveParams sets the client keepalive parameters. When not set, the
// grpc-go defaults apply. Pings more frequent than the server enforcement
// policy allows make the server close the connection.
func WithKeepaliveParams(kp keepalive.ClientParameters) Option {
	return func(cfg *config) error {
		cfg.keepaliveParams = &kp
		return nil
	}
}

// WithMaxRecvMsgSize sets the maximum size in bytes of a message the client
// can receive. When not set, the grpc-go default (4 MiB) applies. Larger
// messages fail the RPC with codes.ResourceExhausted.
func WithMaxRecvMsgSize(n int) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid maxRecvMsgSize")
		}

		cfg.maxRecvMsgSize = n

		return nil
	}
}

// WithMaxSendMsgSize sets the maximum size in bytes of a message the client
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

// WithPerRPCCredentials adds credentials attached to every RPC, for example a
// bearer token source.
func WithPerRPCCredentials(creds credentials.PerRPCCredentials) Option {
	return func(cfg *config) error {
		if creds == nil {
			return errors.New("perRPCCredentials is required")
		}

		cfg.perRPCCredentials = append(cfg.perRPCCredentials, creds)

		return nil
	}
}

// WithStatsHandler adds a stats handler, for example for tracing or metrics.
func WithStatsHandler(h stats.Handler) Option {
	return func(cfg *config) error {
		if h == nil {
			return errors.New("statsHandler is required")
		}

		cfg.statsHandlers = append(cfg.statsHandlers, h)

		return nil
	}
}

// WithInstrumenter adds the dial options of instrumenter, for example a
// metrics or tracing backend. Interceptors added with
// grpc.WithChainUnaryInterceptor or grpc.WithChainStreamInterceptor run after
// the trace propagation and logger interceptors and before the default
// deadline, so they see the propagated trace ID. Its other options follow the
// same rules as WithDialOption. Instrumenters are applied in the order given.
// A nil instrumenter is rejected.
func WithInstrumenter(instrumenter grpcutil.ClientInstrumenter) Option {
	return func(cfg *config) error {
		if instrumenter == nil {
			return errors.New("instrumenter is required")
		}

		cfg.instrumenters = append(cfg.instrumenters, instrumenter)

		return nil
	}
}

// WithUnaryInterceptor adds unary interceptors after the default ones, in the
// provided order. Nil interceptors are rejected.
func WithUnaryInterceptor(interceptors ...grpc.UnaryClientInterceptor) Option {
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
func WithStreamInterceptor(interceptors ...grpc.StreamClientInterceptor) Option {
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

// WithDialOption adds raw grpc.DialOption values, applied after the ones built
// by this package. Use it for settings without a dedicated option, such as a
// custom dialer or resolver. A grpc.WithTransportCredentials option replaces
// the transport security, and a grpc.WithCredentialsBundle option makes
// grpc.NewClient fail; use WithCredentialsBundle instead. Interceptors set
// with grpc.WithUnaryInterceptor or grpc.WithStreamInterceptor run before the
// default interceptor chain; use WithUnaryInterceptor and WithStreamInterceptor
// instead. Nil options are rejected.
func WithDialOption(opts ...grpc.DialOption) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("dialOption is required")
			}
		}

		cfg.extraDialOptions = append(cfg.extraDialOptions, opts...)

		return nil
	}
}
