package grpcclient

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// RPC type values logged in the grpc_type field.
const (
	rpcTypeUnary        = "unary"
	rpcTypeClientStream = "client_stream"
	rpcTypeServerStream = "server_stream"
	rpcTypeBidiStream   = "bidi_stream"
)

// logMessage is the message of the per-RPC log entry. It does not depend on
// the log field prefix.
const logMessage = "outbound"

// traceContext returns ctx with the trace ID stored in the context and set in
// the outgoing metadata. The ID is the valid one stored in ctx, otherwise the
// valid one already set in the outgoing metadata, otherwise a new UUIDv7. The
// outgoing metadata is left unchanged when the ID is already its only value
// under the key.
func (c *config) traceContext(ctx context.Context) context.Context {
	vals := metadata.ValueFromOutgoingContext(ctx, c.traceIDMetadataKey)

	id := traceid.FromContext(ctx, "")
	if !traceid.Valid(id) {
		if len(vals) > 0 && traceid.Valid(vals[0]) {
			id = vals[0]
		} else {
			id = c.rnd.UUIDv7().String()
		}

		ctx = traceid.ForceContext(ctx, id)
	}

	if len(vals) == 1 && vals[0] == id {
		return ctx
	}

	ctx, _ = grpcutil.OutgoingContextWithTraceID(ctx, c.traceIDMetadataKey, "")

	return ctx
}

// unaryTraceInterceptor propagates the trace ID of a unary RPC.
func (c *config) unaryTraceInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	return invoker(c.traceContext(ctx), method, req, reply, cc, opts...)
}

// streamTraceInterceptor propagates the trace ID of a streaming RPC.
func (c *config) streamTraceInterceptor(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	return streamer(c.traceContext(ctx), desc, cc, method, opts...)
}

// unaryDeadlineInterceptor applies the default timeout to a unary RPC whose
// context has no deadline.
func (c *config) unaryDeadlineInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	if _, ok := ctx.Deadline(); ok {
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	return invoker(ctx, method, req, reply, cc, opts...)
}

// unaryLogInterceptor logs one entry per unary RPC.
func (c *config) unaryLogInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	start := time.Now()
	dbglog := c.logger.Enabled(ctx, slog.LevelDebug)

	var reqDump string
	if dbglog {
		reqDump = grpcutil.DumpMessage(req, c.maxDumpSize, c.redactFn)
	}

	err := invoker(ctx, method, req, reply, cc, opts...)

	attrs := c.logAttrs(ctx, cc, method, rpcTypeUnary, start, err)

	if dbglog {
		attrs = append(attrs, slog.String(c.logPrefix+"request", reqDump))

		if err == nil {
			attrs = append(attrs, slog.String(c.logPrefix+"response", grpcutil.DumpMessage(reply, c.maxDumpSize, c.redactFn)))
		}
	}

	c.logRPC(ctx, err, dbglog, attrs)

	return err
}

// streamLogInterceptor logs one entry per streaming RPC, when grpc-go reports
// the end of the stream through grpc.OnFinish.
func (c *config) streamLogInterceptor(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	ls := &loggingStream{
		cfg:           c,
		ctx:           ctx,
		cc:            cc,
		method:        method,
		rpcType:       streamType(desc),
		serverStreams: desc.ServerStreams,
		start:         time.Now(),
	}

	cs, err := streamer(ctx, desc, cc, method, slices.Concat(opts, []grpc.CallOption{grpc.OnFinish(ls.finish)})...)
	if err != nil {
		// An inner interceptor can fail without creating the gRPC stream.
		ls.finish(err)

		return cs, err
	}

	ls.ClientStream = cs

	return ls, nil
}

// loggingStream is a grpc.ClientStream that counts messages and logs the RPC
// once, when it finishes.
type loggingStream struct {
	grpc.ClientStream

	cfg           *config
	ctx           context.Context //nolint:containedctx
	cc            *grpc.ClientConn
	method        string
	rpcType       string
	serverStreams bool
	start         time.Time
	sent          atomic.Int64
	recv          atomic.Int64
	once          sync.Once
}

// SendMsg sends a message and counts it on success.
func (s *loggingStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if err == nil {
		s.sent.Add(1)
	}

	return err //nolint:wrapcheck // gRPC status errors are returned as-is
}

// RecvMsg receives a message and counts it on success.
func (s *loggingStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err == nil {
		s.recv.Add(1)
	}

	return err //nolint:wrapcheck // gRPC status errors are returned as-is
}

// finish logs the RPC with err the first time it is called.
func (s *loggingStream) finish(err error) {
	s.once.Do(func() {
		recv := s.recv.Load()
		if err == nil && !s.serverStreams {
			// grpc-go finishes a stream without server streaming inside the
			// RecvMsg call that receives its only response.
			recv = 1
		}

		c := s.cfg
		attrs := c.logAttrs(s.ctx, s.cc, s.method, s.rpcType, s.start, err)
		attrs = append(attrs,
			slog.Int64(c.logPrefix+"request_messages", s.sent.Load()),
			slog.Int64(c.logPrefix+"response_messages", recv),
		)

		c.logRPC(s.ctx, err, c.logger.Enabled(s.ctx, slog.LevelDebug), attrs)
	})
}

// logAttrs returns the fields of the per-RPC log entry. The redacted error
// message is included when err is not nil.
func (c *config) logAttrs(
	ctx context.Context,
	cc *grpc.ClientConn,
	fullMethod, rpcType string,
	start time.Time,
	err error,
) []slog.Attr {
	end := time.Now()
	service, method := grpcutil.SplitFullMethod(fullMethod)

	var target string
	if cc != nil {
		target = cc.Target()
	}

	attrs := make([]slog.Attr, 0, 14)
	attrs = append(attrs,
		slog.String(c.logPrefix+"component", c.component),
		slog.String(c.logPrefix+traceid.DefaultLogKey, traceid.FromContext(ctx, traceid.DefaultValue)),
		slog.Time(c.logPrefix+"request_time", start.UTC()),
		slog.String(c.logPrefix+"grpc_target", target),
		slog.String(c.logPrefix+"grpc_service", service),
		slog.String(c.logPrefix+"grpc_method", method),
		slog.String(c.logPrefix+"grpc_type", rpcType),
		slog.String(c.logPrefix+"grpc_code", grpcutil.ErrorCode(err).String()),
		slog.Time(c.logPrefix+"response_time", end.UTC()),
		slog.Duration(c.logPrefix+"response_duration", end.Sub(start)),
	)

	if err != nil {
		attrs = append(attrs, slog.String(c.logPrefix+"error", c.redactFn([]byte(err.Error()))))
	}

	return attrs
}

// logRPC writes the per-RPC log entry: at error level when the code of err
// maps to a 5xx HTTP status, otherwise at debug level when debug logging is
// enabled and at info level when it is not.
func (c *config) logRPC(ctx context.Context, err error, dbglog bool, attrs []slog.Attr) {
	level := slog.LevelInfo

	switch {
	case err != nil && grpcutil.HTTPStatusFromCode(grpcutil.ErrorCode(err)) >= http.StatusInternalServerError:
		level = slog.LevelError
	case dbglog:
		level = slog.LevelDebug
	}

	c.logger.LogAttrs(ctx, level, logMessage, attrs...)
}

// streamType returns the grpc_type value for a streaming RPC.
func streamType(desc *grpc.StreamDesc) string {
	switch {
	case desc.ClientStreams && desc.ServerStreams:
		return rpcTypeBidiStream
	case desc.ClientStreams:
		return rpcTypeClientStream
	default:
		return rpcTypeServerStream
	}
}
