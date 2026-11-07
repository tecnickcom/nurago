/*
Package grpcclient builds preconfigured gRPC client connections with trace ID
propagation, structured logging, default deadlines, TLS by default, and native
gRPC retry policies.

[New] returns a plain *grpc.ClientConn created with grpc.NewClient, so
generated stubs and other gRPC tools work unchanged. All the behavior is added
through interceptors and dial options.

# Default Interceptor Chain

Every unary and streaming RPC passes through, outermost first:
  - Trace propagation: the trace ID stored in the context with the traceid
    package is validated and set in the outgoing metadata (default key
    "x-request-id", see [WithTraceIDMetadataKey]). When the context has no
    valid ID, a valid ID already set in the outgoing metadata is used, and
    otherwise a UUIDv7 is generated. The ID is stored in the context with
    traceid.ForceContext, so the inner interceptors see the propagated value.
  - Logger: one "outbound" log entry per RPC (see Logging Behavior).
  - Default deadline: a unary RPC whose context has no deadline gets the one
    set by [WithTimeout] (default 1 minute). Streams are exempt.
  - Custom interceptors added with [WithUnaryInterceptor] and
    [WithStreamInterceptor], in the order given.

The interceptors of the instrumenters added with [WithInstrumenter] run
between the logger and the default deadline.

# Instrumentation

[WithInstrumenter] adds the dial options of a
[github.com/tecnickcom/nurago/pkg/grpcutil.ClientInstrumenter], such as the
Prometheus metrics of the metrics/prometheus/prometheusgrpc package or the
OpenTelemetry spans and metrics of the metrics/opentel/opentelgrpc package.

# Logging Behavior

Each entry carries the component, trace ID, target, service, method, RPC type
(grpc_type), status code name (grpc_code), and timing fields. [WithLogPrefix]
prefixes every field name. A failed RPC also logs its error message, passed
through the [RedactFn].

The entry is logged at error level when the status code maps to a 5xx HTTP
status in google.rpc.Code (Unknown, DeadlineExceeded, Unimplemented, Internal,
Unavailable, DataLoss). Otherwise it is logged at debug level when debug
logging is enabled, and at info level when it is not. Application errors such
as NotFound or InvalidArgument are therefore not logged as errors.

At debug level, unary request and response messages are dumped as protobuf
JSON, redacted with [WithRedactFn], and omitted when larger than
[WithMaxDumpSize]. Streams log the number of messages sent and received
instead. A stream is logged once, when grpc-go reports its end: the final
status is received, a send fails on the client side, or the stream context is
done (logged with codes.Canceled or codes.DeadlineExceeded). As grpc-go
requires, a stream must be read until it returns an error or have its context
canceled; otherwise it is never logged and its resources are not released.

# Transport Security

TLS is enabled by default, with the system root certificates and TLS 1.2 as
the minimum version. [WithTLSConfig] sets a custom configuration, for example
a private CA or a client certificate. Plaintext requires an explicit
[WithInsecure]. [WithCredentialsBundle] replaces TLS with a credentials bundle,
such as Google default, ALTS, or xDS credentials. Combining more than one of
these options makes [New] return [ErrConflictingTransportSecurity].

# Retries

[WithRetryPolicy] compiles retry policies into the default service config, so
retries are executed by grpc-go: they use exponential backoff with jitter,
honor the RPC deadline, and stop once the response headers are received, so a
stream is never replayed after the server has answered. Retry only idempotent
methods. grpc-go caps the attempts of a call at 5, and [WithMaxCallAttempts]
changes the cap. Independently of any policy, grpc-go transparently retries an
RPC that never reached the server application, for example when the connection
fails before the request is sent. For custom retry logic, wrap the call with
github.com/tecnickcom/nurago/pkg/retrier.

# When To Use

  - Outbound gRPC calls should carry the inbound trace ID without threading
    it through every call site.
  - You want one log entry per outbound RPC that redacts secrets by default.
  - You want a client that refuses plaintext unless it is explicitly enabled.
  - You want declarative retries for idempotent RPCs.
*/
package grpcclient

import (
	"errors"
	"fmt"

	"google.golang.org/grpc"
)

// New returns a client connection to target built with grpc.NewClient, the
// default interceptor chain, and the given options.
//
// No connection is established until the first RPC, or until Connect is called
// on the returned connection. Close the connection when it is no longer
// needed. Invalid options return their error, and a target or option rejected
// by grpc.NewClient returns an error wrapping [ErrNewClient].
func New(target string, opts ...Option) (*grpc.ClientConn, error) {
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

	dialOpts, err := cfg.dialOptions()
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNewClient, err)
	}

	return conn, nil
}
