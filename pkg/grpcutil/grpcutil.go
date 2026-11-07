/*
Package grpcutil provides shared gRPC helpers for trace ID propagation through
metadata, for mapping between gRPC status codes and HTTP status codes, for
logging RPCs, and the instrumentation contract of gRPC servers and clients.

# Trace ID Propagation

gRPC carries request-scoped values in metadata instead of HTTP headers. The
helpers bridge metadata and the [github.com/tecnickcom/nurago/pkg/traceid]
context value:

  - [TraceIDFromIncomingContext] reads the trace ID from the incoming metadata
    of a server context and validates it with traceid.Valid. Any value that
    does not match (including an empty or absent key) is replaced with the
    caller-supplied default.
  - [OutgoingContextWithTraceID] reads the trace ID stored in the context with
    traceid.NewContext, validates it, and writes it into the outgoing metadata
    of a client context, replacing any value already set for the same key.
  - [ValidTraceIDMetadataKey] reports whether a metadata key can carry a
    trace ID. Both helpers reject keys that fail it.

[DefaultTraceIDMetadataKey] is traceid.DefaultHeader in metadata form. gRPC
metadata keys are case-insensitive and transmitted in lowercase, so the
default key interoperates with HTTP services using the same header.

# Status Code Mapping

  - [HTTPStatusFromCode] maps a gRPC code to the HTTP status documented for it
    in google.rpc.Code.
  - [CodeFromHTTPStatus] maps an HTTP status returned by application logic to
    the closest gRPC code, so services exposing both transports report the
    same error classes.
  - [ErrorCode] returns the gRPC code of an RPC error, including context
    errors.

# RPC Logging

  - [SplitFullMethod] and [ParseFullMethod] split a "/package.Service/Method"
    name into service and method. ParseFullMethod also rejects malformed names.
  - [DumpMessage] renders a protobuf message as JSON for a log entry, capped
    by encoded size and passed through a [RedactFn].

# Instrumentation

[ServerInstrumenter] and [ClientInstrumenter] are the gRPC counterparts of
the InstrumentHandler and InstrumentRoundTripper methods of
[github.com/tecnickcom/nurago/pkg/metrics.Client]. They supply the server or
dial options that add metrics or tracing to a server or client. A server
instrumenter that also implements [ServerInitializer] is given the server
once every service is registered.

The grpcserver and grpcclient packages accept them with their WithInstrumenter
options. The metrics/prometheus/prometheusgrpc and metrics/opentel/opentelgrpc
packages implement them.

# When To Use

  - A gRPC service or client must carry the same trace ID as the HTTP services
    it calls or is called by.
  - Application errors classified by HTTP status must be returned over gRPC,
    or the other way around.
  - A gRPC server or client interceptor logs RPCs.
  - A metrics or tracing backend must plug into gRPC servers and clients.
*/
package grpcutil

import (
	"context"
	"strings"

	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc/metadata"
)

// DefaultTraceIDMetadataKey is the default metadata key for the trace ID.
// It is the lowercase form of traceid.DefaultHeader.
const DefaultTraceIDMetadataKey = "x-request-id"

// ValidTraceIDMetadataKey reports whether key can carry a trace ID as ASCII
// metadata. Matching is case-insensitive. A valid key is non-empty, contains
// only [0-9a-z._-], does not start with "grpc-", does not end with "-bin", and
// is not a reserved key: "content-type", "user-agent", "te", "host", or an
// HTTP/2 connection-specific header ("connection", "keep-alive",
// "proxy-connection", "transfer-encoding", "upgrade").
func ValidTraceIDMetadataKey(key string) bool {
	k := strings.ToLower(key)

	switch {
	case k == "",
		strings.HasPrefix(k, "grpc-"),
		strings.HasSuffix(k, "-bin"),
		isReservedMetadataKey(k):
		return false
	}

	for i := range len(k) {
		if !isMetadataKeyByte(k[i]) {
			return false
		}
	}

	return true
}

// isReservedMetadataKey reports whether the lowercase key k is used by gRPC or
// HTTP/2 and cannot carry application metadata.
func isReservedMetadataKey(k string) bool {
	switch k {
	case "content-type", "user-agent", "te", "host",
		"connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade":
		return true
	}

	return false
}

// isMetadataKeyByte reports whether c is allowed in a lowercase metadata key.
func isMetadataKeyByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || c == '-' || c == '_' || c == '.'
}

// TraceIDFromIncomingContext returns the trace ID found under key in the
// incoming metadata of ctx, validated with traceid.Valid. It returns
// defaultValue when key fails [ValidTraceIDMetadataKey], or when the key is
// absent or its first value is invalid.
func TraceIDFromIncomingContext(ctx context.Context, key, defaultValue string) string {
	if !ValidTraceIDMetadataKey(key) {
		return defaultValue
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return defaultValue
	}

	vals := md.Get(key)
	if len(vals) == 0 || !traceid.Valid(vals[0]) {
		return defaultValue
	}

	return vals[0]
}

// OutgoingContextWithTraceID sets the trace ID stored in ctx (see
// traceid.FromContext) under key in the outgoing metadata, replacing any value
// already set for key, and returns the new context and the ID that was set.
//
// An ID that fails traceid.Valid is replaced with defaultValue, which is
// validated the same way. When the final value is invalid, or key fails
// [ValidTraceIDMetadataKey], ctx is returned unchanged together with an empty
// string.
func OutgoingContextWithTraceID(ctx context.Context, key, defaultValue string) (context.Context, string) {
	if !ValidTraceIDMetadataKey(key) {
		return ctx, ""
	}

	id := traceid.FromContext(ctx, defaultValue)

	if !traceid.Valid(id) {
		id = defaultValue
	}

	if !traceid.Valid(id) {
		return ctx, ""
	}

	md, _ := metadata.FromOutgoingContext(ctx) // returns a copy
	if md == nil {
		md = metadata.MD{}
	}

	md.Set(key, id)

	return metadata.NewOutgoingContext(ctx, md), id
}
