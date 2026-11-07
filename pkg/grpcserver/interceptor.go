package grpcserver

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/tap"
)

// RPC type values logged in the grpc_type field.
const (
	rpcTypeUnary        = "unary"
	rpcTypeClientStream = "client_stream"
	rpcTypeServerStream = "server_stream"
	rpcTypeBidiStream   = "bidi_stream"
)

// logMessage is the message of the per-RPC log entry.
const logMessage = "request"

// valuesContext is an RPC context that falls back to values copied from the
// New context. Deadline, cancelation, and errors come from the RPC context.
type valuesContext struct {
	context.Context //nolint:containedctx

	values []ctxValue
}

// Value returns the value for key from the RPC context, or from values when
// the RPC context has none.
func (c valuesContext) Value(key any) any {
	v := c.Context.Value(key)
	if v != nil {
		return v
	}

	for _, kv := range c.values {
		if kv.key == key {
			return kv.val
		}
	}

	return nil
}

// wrappedStream is a grpc.ServerStream with a replaced context.
type wrappedStream struct {
	grpc.ServerStream

	ctx context.Context //nolint:containedctx
}

// Context returns the replaced stream context.
func (s *wrappedStream) Context() context.Context {
	return s.ctx
}

// countingStream is a wrappedStream that counts received and sent messages.
type countingStream struct {
	wrappedStream

	recv atomic.Int64
	sent atomic.Int64
}

// RecvMsg receives a message and counts it on success.
func (s *countingStream) RecvMsg(m any) error {
	err := s.ServerStream.RecvMsg(m)
	if err == nil {
		s.recv.Add(1)
	}

	return err //nolint:wrapcheck
}

// SendMsg sends a message and counts it on success.
func (s *countingStream) SendMsg(m any) error {
	err := s.ServerStream.SendMsg(m)
	if err == nil {
		s.sent.Add(1)
	}

	return err //nolint:wrapcheck
}

// contextValues returns the non-nil values stored in ctx under keys.
func contextValues(ctx context.Context, keys []any) []ctxValue {
	var vals []ctxValue

	for _, k := range keys {
		if v := ctx.Value(k); v != nil {
			vals = append(vals, ctxValue{key: k, val: v})
		}
	}

	return vals
}

// injectRequest returns the RPC context with the New context values and the trace ID,
// and the trace ID itself. A missing or invalid incoming ID is replaced with a
// new UUIDv7.
func (c *config) injectRequest(ctx context.Context) (context.Context, string) {
	reqID := grpcutil.TraceIDFromIncomingContext(ctx, c.traceIDMetadataKey, "")
	if reqID == "" {
		reqID = c.rnd.UUIDv7().String()
	}

	if len(c.baseValues) > 0 {
		ctx = valuesContext{Context: ctx, values: c.baseValues}
	}

	// ForceContext, not NewContext: the context carries the ID that is logged
	// and returned, even when an earlier interceptor stored another one.
	ctx = traceid.ForceContext(ctx, reqID)

	return ctx, reqID
}

// unaryRequestInterceptor injects the trace ID and logs one entry per unary
// RPC. A panic raised by its own code is handled by requestPanic.
func (c *config) unaryRequestInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp any, err error) { //nolint:nonamedreturns // the deferred recover keeps the handler result
	var handled bool

	defer func() {
		if p := recover(); p != nil {
			err = c.requestPanic(ctx, info.FullMethod, p, handled, err)
			if !handled {
				resp = nil
			}
		}
	}()

	reqTime := time.Now()

	ctx, reqID := c.injectRequest(ctx)

	// Fails only outside a real RPC or after headers are sent; neither applies here.
	_ = grpc.SetHeader(ctx, metadata.Pairs(c.traceIDMetadataKey, reqID))

	if !c.isLoggerEnabled(info.FullMethod) {
		return handler(ctx, req)
	}

	attrs := c.requestAttrs(ctx, reqID, reqTime, info.FullMethod, rpcTypeUnary)

	dbglog := c.logger.Enabled(ctx, slog.LevelDebug)
	if dbglog {
		attrs = append(attrs, slog.String("request_dump", grpcutil.DumpMessage(req, c.maxDumpSize, c.redactFn)))
	}

	resp, err = handler(ctx, req)
	handled = true

	attrs = append(attrs, c.responseAttrs(reqTime, err, dbglog)...)

	if dbglog {
		attrs = append(attrs, slog.String("response_dump", grpcutil.DumpMessage(resp, c.maxDumpSize, c.redactFn)))
	}

	c.logRequest(ctx, dbglog, attrs)

	return resp, err
}

// streamRequestInterceptor injects the trace ID and logs one entry per
// streaming RPC. A panic raised by its own code is handled by requestPanic.
func (c *config) streamRequestInterceptor(
	srv any,
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	var handled bool

	ctx := ss.Context()

	defer func() {
		if p := recover(); p != nil {
			err = c.requestPanic(ctx, info.FullMethod, p, handled, err)
		}
	}()

	reqTime := time.Now()

	ctx, reqID := c.injectRequest(ctx)

	// Fails only after headers are sent, which cannot happen before the handler runs.
	_ = ss.SetHeader(metadata.Pairs(c.traceIDMetadataKey, reqID))

	if !c.isLoggerEnabled(info.FullMethod) {
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}

	cs := &countingStream{wrappedStream: wrappedStream{ServerStream: ss, ctx: ctx}}

	err = handler(srv, cs)
	handled = true

	dbglog := c.logger.Enabled(ctx, slog.LevelDebug)

	attrs := c.requestAttrs(ctx, reqID, reqTime, info.FullMethod, streamType(info))
	attrs = append(attrs, c.responseAttrs(reqTime, err, dbglog)...)
	attrs = append(attrs,
		slog.Int64("request_messages", cs.recv.Load()),
		slog.Int64("response_messages", cs.sent.Load()),
	)

	c.logRequest(ctx, dbglog, attrs)

	return err
}

// unaryRecoveryInterceptor converts a handler panic into an error.
func (c *config) unaryRecoveryInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	var resp any

	err := c.runRecovering(ctx, info.FullMethod, func() error {
		var herr error

		resp, herr = handler(ctx, req)

		return herr
	})

	return resp, err
}

// streamRecoveryInterceptor converts a handler panic into an error.
func (c *config) streamRecoveryInterceptor(
	srv any,
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	return c.runRecovering(ss.Context(), info.FullMethod, func() error {
		return handler(srv, ss)
	})
}

// runRecovering calls fn and converts a panic into the error returned by
// recoverPanic.
func (c *config) runRecovering(ctx context.Context, fullMethod string, fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = c.recoverPanic(ctx, fullMethod, p)
		}
	}()

	return fn()
}

// requestPanic handles a panic raised by a request interceptor outside the
// handler call, for example by a RedactFn or a log handler. Before the handler
// has run, it returns the recoverPanic error. After the handler has run, it
// logs the panic and returns herr, the handler error.
func (c *config) requestPanic(ctx context.Context, fullMethod string, p any, handled bool, herr error) error {
	if !handled {
		return c.recoverPanic(ctx, fullMethod, p)
	}

	c.logPanic(ctx, fullMethod, "request logger panic", p, string(debug.Stack()))

	return herr
}

// recoverPanic logs a recovered panic with its stack trace and returns the
// error produced by the panic handler. A panic raised by the panic handler is
// logged and answered with the default error. A panic raised by the logger is
// discarded.
func (c *config) recoverPanic(ctx context.Context, fullMethod string, p any) error {
	c.logPanic(ctx, fullMethod, "panic", p, string(debug.Stack()))

	var err error

	hp := catchPanic(func() { err = c.panicHandlerFunc(ctx, p) })
	if hp != nil {
		c.logPanic(ctx, fullMethod, "panic handler panic", hp, "")
	}

	if err == nil {
		return defaultPanicHandler(ctx, p)
	}

	return err
}

// logPanic logs the panic value p at error level with msg, and with stack when
// it is not empty. A panic raised by the logger is discarded.
func (c *config) logPanic(ctx context.Context, fullMethod, msg string, p any, stack string) {
	service, method := grpcutil.SplitFullMethod(fullMethod)

	attrs := []slog.Attr{
		slog.String(traceid.DefaultLogKey, traceid.FromContext(ctx, traceid.DefaultValue)),
		slog.String("grpc_service", service),
		slog.String("grpc_method", method),
		slog.Any("error", p),
	}

	if stack != "" {
		attrs = append(attrs, slog.String("stacktrace", stack))
	}

	catchPanic(func() { c.logger.LogAttrs(ctx, slog.LevelError, msg, attrs...) })
}

// catchPanic calls fn and returns the value of a panic raised by fn, or nil.
func catchPanic(fn func()) any {
	var p any

	func() {
		defer func() { p = recover() }()

		fn()
	}()

	return p
}

// unaryDeadlineInterceptor applies the request timeout to a unary RPC that has
// no deadline.
func (c *config) unaryDeadlineInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	timeout := c.methodTimeout(info.FullMethod, c.requestTimeout)
	if timeout <= 0 {
		return handler(ctx, req)
	}

	if _, ok := ctx.Deadline(); ok {
		return handler(ctx, req)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return handler(ctx, req)
}

// methodDeadlineTap applies the per-method timeout to an RPC that has no
// deadline. It runs in the transport before the stream is created, so the
// deadline also ends blocked stream reads and writes. Unlike a client
// deadline, it does not reset the stream: an RPC whose handler ignores the
// cancelation stays open until the handler returns.
func (c *config) methodDeadlineTap(ctx context.Context, info *tap.Info) (context.Context, error) {
	timeout := c.methodTimeouts[info.FullMethodName]
	if timeout <= 0 {
		return ctx, nil
	}

	if _, ok := ctx.Deadline(); ok {
		return ctx, nil
	}

	ctx, cancel := context.WithTimeout(ctx, timeout) //nolint:gosec // released when the transport cancels the parent stream context
	_ = cancel

	return ctx, nil
}

// requestAttrs returns the request fields of the per-RPC log entry.
func (c *config) requestAttrs(ctx context.Context, reqID string, reqTime time.Time, fullMethod, rpcType string) []slog.Attr {
	service, method := grpcutil.SplitFullMethod(fullMethod)

	attrs := make([]slog.Attr, 0, 14)

	return append(attrs,
		slog.String(traceid.DefaultLogKey, reqID),
		slog.Time("request_time", reqTime.UTC()),
		slog.String("grpc_service", service),
		slog.String("grpc_method", method),
		slog.String("grpc_type", rpcType),
		slog.String("request_remote_address", peerAddr(ctx)),
		slog.String("request_user_agent", userAgent(ctx)),
	)
}

// responseAttrs returns the response fields of the per-RPC log entry. The
// redacted error message is included only at debug level.
func (c *config) responseAttrs(reqTime time.Time, err error, dbglog bool) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("grpc_code", grpcutil.ErrorCode(err).String()),
		slog.Duration("response_duration", time.Since(reqTime)),
	}

	if dbglog && err != nil {
		attrs = append(attrs, slog.String("error", c.redactFn([]byte(err.Error()))))
	}

	return attrs
}

// logRequest writes the per-RPC log entry, at debug level when debug logging
// is enabled and at info level otherwise.
func (c *config) logRequest(ctx context.Context, dbglog bool, attrs []slog.Attr) {
	level := slog.LevelInfo
	if dbglog {
		level = slog.LevelDebug
	}

	c.logger.LogAttrs(ctx, level, logMessage, attrs...)
}

// streamType returns the grpc_type value for a streaming RPC.
func streamType(info *grpc.StreamServerInfo) string {
	switch {
	case info.IsClientStream && info.IsServerStream:
		return rpcTypeBidiStream
	case info.IsClientStream:
		return rpcTypeClientStream
	default:
		return rpcTypeServerStream
	}
}

// peerAddr returns the remote address of the RPC peer, or an empty string.
func peerAddr(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}

	return p.Addr.String()
}

// userAgent returns the first user-agent value of the incoming metadata, or an
// empty string.
func userAgent(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)

	if v := md.Get("user-agent"); len(v) > 0 {
		return v[0]
	}

	return ""
}
