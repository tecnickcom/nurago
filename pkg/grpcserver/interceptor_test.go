package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

// withTraceID returns ctx carrying id in the outgoing default trace ID metadata.
func withTraceID(ctx context.Context, id string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, id)
}

func TestTraceID_unary(t *testing.T) {
	t.Parallel()

	_, client := newTestServer(t, &testService{})

	t.Run("propagated and returned in the header", func(t *testing.T) {
		t.Parallel()

		var header metadata.MD

		resp, err := client.UnaryCall(withTraceID(rpcContext(t), "abc-123"), &testgrpc.SimpleRequest{}, grpc.Header(&header))
		require.NoError(t, err)
		require.Equal(t, "abc-123", resp.GetUsername())
		require.Equal(t, []string{"abc-123"}, header.Get(grpcutil.DefaultTraceIDMetadataKey))
	})

	t.Run("generated when missing", func(t *testing.T) {
		t.Parallel()

		var header metadata.MD

		resp, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{}, grpc.Header(&header))
		require.NoError(t, err)
		require.Len(t, resp.GetUsername(), 36)
		require.True(t, traceid.Valid(resp.GetUsername()))
		require.Equal(t, []string{resp.GetUsername()}, header.Get(grpcutil.DefaultTraceIDMetadataKey))
	})

	t.Run("replaced when invalid", func(t *testing.T) {
		t.Parallel()

		resp, err := client.UnaryCall(withTraceID(rpcContext(t), "bad id"), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.NotEqual(t, "bad id", resp.GetUsername())
		require.Len(t, resp.GetUsername(), 36)
	})
}

func TestTraceID_stream(t *testing.T) {
	t.Parallel()

	_, client := newTestServer(t, &testService{})

	stream, err := client.StreamingOutputCall(
		withTraceID(rpcContext(t), "stream-id"),
		&testgrpc.StreamingOutputCallRequest{ResponseParameters: []*testgrpc.ResponseParameters{{}}},
	)
	require.NoError(t, err)

	header, err := stream.Header()
	require.NoError(t, err)
	require.Equal(t, []string{"stream-id"}, header.Get(grpcutil.DefaultTraceIDMetadataKey))

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(resp.GetPayload().GetBody()), "stream-id|"))
}

func TestTraceID_customMetadataKey(t *testing.T) {
	t.Parallel()

	_, client := newTestServer(t, &testService{}, WithTraceIDMetadataKey("X-Trace-Id"))

	var header metadata.MD

	ctx := metadata.AppendToOutgoingContext(rpcContext(t), "x-trace-id", "custom-123")

	resp, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, "custom-123", resp.GetUsername())
	require.Equal(t, []string{"custom-123"}, header.Get("x-trace-id"))
}

func TestContextValues_rpc(t *testing.T) {
	t.Parallel()

	type otherKey struct{}

	newContext := func(t *testing.T) context.Context {
		t.Helper()

		ctx := context.WithValue(t.Context(), baseCtxKey{}, "base-value")
		ctx = context.WithValue(ctx, otherKey{}, "other")

		return traceid.NewContext(ctx, "base-trace-id")
	}

	t.Run("listed key", func(t *testing.T) {
		t.Parallel()

		_, client := newTestServerCtx(t, newContext(t), &testService{}, WithContextValues(baseCtxKey{}))

		resp, err := client.UnaryCall(withTraceID(rpcContext(t), "rpc-trace-id"), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.Equal(t, "base-value", resp.GetServerId())
		require.Equal(t, "rpc-trace-id", resp.GetUsername())

		resp, err = client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.NotEqual(t, "base-trace-id", resp.GetUsername())
	})

	t.Run("no keys", func(t *testing.T) {
		t.Parallel()

		_, client := newTestServerCtx(t, newContext(t), &testService{})

		resp, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.Empty(t, resp.GetServerId(), "unlisted values of the New context must not reach RPC contexts")
	})
}

func TestContextValues(t *testing.T) {
	t.Parallel()

	type key struct{ name string }

	ctx := context.WithValue(t.Context(), key{"a"}, "va")
	ctx = context.WithValue(ctx, key{"b"}, "vb")

	require.Nil(t, contextValues(ctx, nil))
	require.Nil(t, contextValues(ctx, []any{key{"missing"}}))
	require.Equal(t,
		[]ctxValue{{key: key{"a"}, val: "va"}, {key: key{"b"}, val: "vb"}},
		contextValues(ctx, []any{key{"a"}, key{"missing"}, key{"b"}}),
	)
}

func TestValuesContext(t *testing.T) {
	t.Parallel()

	type key struct{ name string }

	rpc, cancel := context.WithCancel(context.WithValue(t.Context(), key{"shared"}, "rpc"))

	ctx := valuesContext{Context: rpc, values: []ctxValue{
		{key: key{"shared"}, val: "base"},
		{key: key{"base-only"}, val: "base"},
	}}

	require.Equal(t, "rpc", ctx.Value(key{"shared"}), "the RPC value takes precedence")
	require.Equal(t, "base", ctx.Value(key{"base-only"}))
	require.Nil(t, ctx.Value(key{"missing"}))
	require.Nil(t, ctx.Value([]int{1}), "a non-comparable key does not panic")

	child, childCancel := context.WithCancel(ctx)
	defer childCancel()

	cancel()

	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		require.Fail(t, "canceling the RPC context must cancel derived contexts")
	}
}

func TestRequestLogger_unary(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	_, client := newTestServer(t, &testService{}, WithLogger(logger))

	_, err := client.UnaryCall(withTraceID(rpcContext(t), "log-id"), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	_, err = client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound), Message: "missing"},
	})
	require.Equal(t, codes.NotFound, status.Code(err))

	entries := sink.entries(t, logMessage)
	require.Len(t, entries, 2)

	e := entries[0]
	require.Equal(t, slog.LevelInfo.String(), e[slog.LevelKey])
	require.Equal(t, "grpcserver", e["component"])
	require.Equal(t, "log-id", e[traceid.DefaultLogKey])
	require.Equal(t, "grpc.testing.TestService", e["grpc_service"])
	require.Equal(t, "UnaryCall", e["grpc_method"])
	require.Equal(t, rpcTypeUnary, e["grpc_type"])
	require.Equal(t, codes.OK.String(), e["grpc_code"])
	require.True(t, strings.HasSuffix(e["request_time"].(string), "Z"), "request_time is logged in UTC") //nolint:forcetypeassert
	require.NotEmpty(t, e["response_duration"])
	require.Contains(t, e["request_remote_address"], "127.0.0.1:")
	require.Contains(t, e["request_user_agent"], "grpc-go/")
	require.NotContains(t, e, "request_dump")
	require.NotContains(t, e, "error")

	require.Equal(t, codes.NotFound.String(), entries[1]["grpc_code"])
	require.NotContains(t, entries[1], "error", "error messages are logged at debug level only")
}

func TestRequestLogger_unaryDebug(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)
	redactFn := func(b []byte) string { return "redacted:" + string(b) }

	_, client := newTestServer(t, &testService{}, WithLogger(logger), WithRedactFn(redactFn))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{FillUsername: true})
	require.NoError(t, err)

	_, err = client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.InvalidArgument), Message: "bad input"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	entries := sink.entries(t, logMessage)
	require.Len(t, entries, 2)

	e := entries[0]
	require.Equal(t, slog.LevelDebug.String(), e[slog.LevelKey])
	require.Contains(t, e["request_dump"], "redacted:")
	require.Contains(t, e["request_dump"], "fillUsername")
	require.Contains(t, e["response_dump"], "redacted:")
	require.Contains(t, e["response_dump"], "username")
	require.NotContains(t, e, "error")

	e = entries[1]
	require.Equal(t, codes.InvalidArgument.String(), e["grpc_code"])
	require.Equal(t, "redacted:rpc error: code = InvalidArgument desc = bad input", e["error"])
	require.Empty(t, e["response_dump"], "a failed RPC has no response message")
}

func TestRequestLogger_streams(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	_, client := newTestServer(t, &testService{}, WithLogger(logger))

	// server stream: 1 request, 3 responses
	out, err := client.StreamingOutputCall(rpcContext(t), &testgrpc.StreamingOutputCallRequest{
		ResponseParameters: []*testgrpc.ResponseParameters{{}, {}, {}},
	})
	require.NoError(t, err)

	for range 3 {
		_, err = out.Recv()
		require.NoError(t, err)
	}

	_, err = out.Recv()
	require.ErrorIs(t, err, io.EOF)

	// client stream: 2 requests, 1 response
	in, err := client.StreamingInputCall(rpcContext(t))
	require.NoError(t, err)
	require.NoError(t, in.Send(&testgrpc.StreamingInputCallRequest{}))
	require.NoError(t, in.Send(&testgrpc.StreamingInputCallRequest{}))

	inResp, err := in.CloseAndRecv()
	require.NoError(t, err)
	require.EqualValues(t, 2, inResp.GetAggregatedPayloadSize())

	// bidi stream: 2 requests, 2 responses
	bidi, err := client.FullDuplexCall(rpcContext(t))
	require.NoError(t, err)

	for range 2 {
		require.NoError(t, bidi.Send(&testgrpc.StreamingOutputCallRequest{}))

		_, err = bidi.Recv()
		require.NoError(t, err)
	}

	require.NoError(t, bidi.CloseSend())

	_, err = bidi.Recv()
	require.ErrorIs(t, err, io.EOF)

	entries := sink.entries(t, logMessage)
	require.Len(t, entries, 3)

	byMethod := make(map[string]map[string]any, len(entries))
	for _, e := range entries {
		byMethod[e["grpc_method"].(string)] = e //nolint:forcetypeassert
	}

	tests := []struct {
		method   string
		rpcType  string
		recv     float64
		sent     float64
		wantCode codes.Code
	}{
		{method: "StreamingOutputCall", rpcType: rpcTypeServerStream, recv: 1, sent: 3},
		{method: "StreamingInputCall", rpcType: rpcTypeClientStream, recv: 2, sent: 1},
		{method: "FullDuplexCall", rpcType: rpcTypeBidiStream, recv: 2, sent: 2},
	}

	for _, tt := range tests {
		e := byMethod[tt.method]
		require.NotNil(t, e, tt.method)
		require.Equal(t, tt.rpcType, e["grpc_type"], tt.method)
		require.InDelta(t, tt.recv, e["request_messages"], 0, tt.method)
		require.InDelta(t, tt.sent, e["response_messages"], 0, tt.method)
		require.Equal(t, codes.OK.String(), e["grpc_code"], tt.method)
		require.NotEmpty(t, e[traceid.DefaultLogKey], tt.method)
	}
}

func TestRequestLogger_streamDebugError(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)

	_, client := newTestServer(t, &testService{}, WithLogger(logger))

	bidi, err := client.FullDuplexCall(rpcContext(t))
	require.NoError(t, err)
	require.NoError(t, bidi.Send(&testgrpc.StreamingOutputCallRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.Aborted), Message: "stop"},
	}))

	_, err = bidi.Recv()
	require.Equal(t, codes.Aborted, status.Code(err))

	entries := sink.entries(t, logMessage)
	require.Len(t, entries, 1)
	require.Equal(t, slog.LevelDebug.String(), entries[0][slog.LevelKey])
	require.Equal(t, codes.Aborted.String(), entries[0]["grpc_code"])
	require.Contains(t, entries[0]["error"], "stop")
	require.NotContains(t, entries[0], "request_dump", "stream messages are not dumped")
}

func TestRequestLogger_disabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opt         Option
		wantEntries int
	}{
		{name: "all methods", opt: WithoutRequestLogger(), wantEntries: 0},
		{name: "unary method only", opt: WithoutMethodLogger(methodUnaryCall), wantEntries: 1},
		{name: "stream method only", opt: WithoutMethodLogger(methodStreamingOutputCall), wantEntries: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, sink := newTestLogger(slog.LevelDebug)

			_, client := newTestServer(t, &testService{}, WithLogger(logger), tt.opt)

			resp, err := client.UnaryCall(withTraceID(rpcContext(t), "still-injected"), &testgrpc.SimpleRequest{})
			require.NoError(t, err)
			require.Equal(t, "still-injected", resp.GetUsername(), "trace ID injection is independent of logging")

			stream, err := client.StreamingOutputCall(withTraceID(rpcContext(t), "stream-injected"), &testgrpc.StreamingOutputCallRequest{
				ResponseParameters: []*testgrpc.ResponseParameters{{}},
			})
			require.NoError(t, err)

			msg, err := stream.Recv()
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(string(msg.GetPayload().GetBody()), "stream-injected|"))

			_, err = stream.Recv()
			require.ErrorIs(t, err, io.EOF)

			require.Len(t, sink.entries(t, logMessage), tt.wantEntries)
		})
	}
}

func TestRecovery(t *testing.T) {
	t.Parallel()

	panicReq := &testgrpc.EchoStatus{Code: panicCode}

	tests := []struct {
		name     string
		opts     []Option
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name:     "default handler",
			wantCode: codes.Internal,
			wantMsg:  codes.Internal.String(),
		},
		{
			name: "custom handler",
			opts: []Option{WithPanicHandlerFunc(func(_ context.Context, p any) error {
				return status.Errorf(codes.Unavailable, "recovered: %v", p)
			})},
			wantCode: codes.Unavailable,
			wantMsg:  "recovered: test panic",
		},
		{
			name: "custom handler returning nil",
			opts: []Option{WithPanicHandlerFunc(func(_ context.Context, _ any) error {
				return nil
			})},
			wantCode: codes.Internal,
			wantMsg:  codes.Internal.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, sink := newTestLogger(slog.LevelInfo)

			_, client := newTestServer(t, &testService{}, append([]Option{WithLogger(logger)}, tt.opts...)...)

			_, err := client.UnaryCall(withTraceID(rpcContext(t), "panic-id"), &testgrpc.SimpleRequest{ResponseStatus: panicReq})
			require.Equal(t, tt.wantCode, status.Code(err))
			require.Equal(t, tt.wantMsg, status.Convert(err).Message())

			stream, err := client.StreamingOutputCall(rpcContext(t), &testgrpc.StreamingOutputCallRequest{ResponseStatus: panicReq})
			require.NoError(t, err)

			_, err = stream.Recv()
			require.Equal(t, tt.wantCode, status.Code(err))

			panics := sink.entries(t, "panic")
			require.Len(t, panics, 2)
			require.Equal(t, slog.LevelError.String(), panics[0][slog.LevelKey])
			require.Equal(t, "test panic", panics[0]["error"])
			require.Contains(t, panics[0]["stacktrace"], "runtime/debug.Stack")
			require.Equal(t, "UnaryCall", panics[0]["grpc_method"])
			require.Equal(t, "panic-id", panics[0][traceid.DefaultLogKey])
			require.Equal(t, "StreamingOutputCall", panics[1]["grpc_method"])

			requests := sink.entries(t, logMessage)
			require.Len(t, requests, 2)

			for _, e := range requests {
				require.Equal(t, tt.wantCode.String(), e["grpc_code"], "the request log records the recovered code")
			}
		})
	}
}

func TestRecovery_panickingPanicHandler(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	_, client := newTestServer(t, &testService{}, WithLogger(logger), WithPanicHandlerFunc(func(_ context.Context, p any) error {
		return p.(error) //nolint:forcetypeassert // panics on the string panic value
	}))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{ResponseStatus: &testgrpc.EchoStatus{Code: panicCode}})
	require.Equal(t, codes.Internal, status.Code(err))

	require.Len(t, sink.entries(t, "panic"), 1)

	nested := sink.entries(t, "panic handler panic")
	require.Len(t, nested, 1)
	require.Equal(t, "UnaryCall", nested[0]["grpc_method"])
	require.Contains(t, nested[0]["error"], "interface conversion")
}

// panickingHandler is a slog.Handler that panics on every record.
type panickingHandler struct {
	slog.Handler
}

func (panickingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (panickingHandler) Handle(context.Context, slog.Record) error { panic("logger panic") }

func TestRecoverPanic_panickingLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.logger = slog.New(panickingHandler{})

	called := false
	cfg.panicHandlerFunc = func(_ context.Context, _ any) error {
		called = true

		panic("handler panic")
	}

	var err error

	require.NotPanics(t, func() { err = cfg.recoverPanic(t.Context(), methodUnaryCall, "boom") })
	require.True(t, called)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestRequestPanic_unary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		panicCall int32 // RedactFn call that panics: 1 is the request dump, 2 the response dump
		wantCode  codes.Code
		wantMsg   string
	}{
		{
			name:      "before the handler",
			panicCall: 1,
			wantCode:  codes.Internal,
			wantMsg:   "panic",
		},
		{
			name:      "after the handler",
			panicCall: 2,
			wantCode:  codes.OK,
			wantMsg:   "request logger panic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			redactFn := func(b []byte) string {
				if calls.Add(1) == tt.panicCall {
					panic("redact panic")
				}

				return string(b)
			}

			logger, sink := newTestLogger(slog.LevelDebug)

			_, client := newTestServer(t, &testService{}, WithLogger(logger), WithRedactFn(redactFn))

			resp, err := client.UnaryCall(withTraceID(rpcContext(t), "redact-id"), &testgrpc.SimpleRequest{})
			require.Equal(t, tt.wantCode, status.Code(err))

			if tt.wantCode == codes.OK {
				require.Equal(t, "redact-id", resp.GetUsername())
			}

			panics := sink.entries(t, tt.wantMsg)
			require.Len(t, panics, 1)
			require.Equal(t, "redact panic", panics[0]["error"])
			require.Equal(t, "UnaryCall", panics[0]["grpc_method"])
			require.Contains(t, panics[0]["stacktrace"], "runtime/debug.Stack")
			require.Empty(t, sink.entries(t, logMessage))
		})
	}
}

func TestRequestPanic_streamAfterHandler(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)

	redactFn := func(_ []byte) string { panic("redact panic") }

	_, client := newTestServer(t, &testService{}, WithLogger(logger), WithRedactFn(redactFn))

	stream, err := client.StreamingOutputCall(rpcContext(t), &testgrpc.StreamingOutputCallRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound), Message: "missing"},
	})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, status.Code(err), "the handler error is returned")

	panics := sink.entries(t, "request logger panic")
	require.Len(t, panics, 1)
	require.Equal(t, "StreamingOutputCall", panics[0]["grpc_method"])
	require.Empty(t, sink.entries(t, logMessage))
}

// headerPanicStream is a grpc.ServerStream whose SetHeader panics.
type headerPanicStream struct {
	grpc.ServerStream

	ctx context.Context //nolint:containedctx
}

func (s headerPanicStream) Context() context.Context { return s.ctx }

func (headerPanicStream) SetHeader(metadata.MD) error { panic("header panic") }

func TestRequestPanic_streamBeforeHandler(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	cfg := defaultConfig()
	cfg.logger = logger

	called := false
	info := &grpc.StreamServerInfo{FullMethod: methodFullDuplexCall, IsClientStream: true, IsServerStream: true}

	var err error

	require.NotPanics(t, func() {
		err = cfg.streamRequestInterceptor(nil, headerPanicStream{ctx: t.Context()}, info, func(_ any, _ grpc.ServerStream) error {
			called = true
			return nil
		})
	})
	require.Equal(t, codes.Internal, status.Code(err))
	require.False(t, called)

	panics := sink.entries(t, "panic")
	require.Len(t, panics, 1)
	require.Equal(t, "header panic", panics[0]["error"])
	require.Equal(t, "FullDuplexCall", panics[0]["grpc_method"])
}

func TestRequestLogger_contextErrorCode(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	_, client := newTestServer(t, &testService{}, WithLogger(logger), WithUnaryInterceptor(
		func(ctx context.Context, _ any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
			c, cancel := context.WithTimeout(ctx, time.Nanosecond)
			defer cancel()

			<-c.Done()

			return nil, fmt.Errorf("handler: %w", c.Err())
		},
	))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))

	entries := sink.entries(t, logMessage)
	require.Len(t, entries, 1)
	require.Equal(t, codes.DeadlineExceeded.String(), entries[0]["grpc_code"], "the log records the code sent to the client")
}

func TestMethodDeadlineTap(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	require.NoError(t, WithMethodTimeout(methodFullDuplexCall, time.Minute)(cfg))
	require.NoError(t, WithMethodTimeout(methodUnaryCall, -1)(cfg))

	for _, m := range []string{methodUnaryCall, methodStreamingOutputCall} {
		ctx, err := cfg.methodDeadlineTap(t.Context(), &tap.Info{FullMethodName: m})
		require.NoError(t, err)

		_, ok := ctx.Deadline()
		require.False(t, ok, m)
	}

	ctx, err := cfg.methodDeadlineTap(t.Context(), &tap.Info{FullMethodName: methodFullDuplexCall})
	require.NoError(t, err)
	requireDeadline(t, deadlineInfo(ctx), 59*time.Second, time.Minute)

	clientCtx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()

	ctx, err = cfg.methodDeadlineTap(clientCtx, &tap.Info{FullMethodName: methodFullDuplexCall})
	require.NoError(t, err)
	require.Equal(t, clientCtx, ctx, "an existing deadline is kept")
}

func TestDeadline_streamBlockedRecv(t *testing.T) {
	t.Parallel()

	_, client := newTestServer(t, &testService{}, WithMethodTimeout(methodFullDuplexCall, 200*time.Millisecond))

	// No client deadline, and no message sent: the handler blocks in Recv.
	stream, err := client.FullDuplexCall(t.Context())
	require.NoError(t, err)

	done := make(chan error, 1)

	go func() {
		_, err := stream.Recv()
		done <- err
	}()

	select {
	case err := <-done:
		require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	case <-time.After(10 * time.Second):
		require.Fail(t, "the method timeout must end a stream blocked in Recv")
	}
}

func TestDeadline_unary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		opts           []Option
		clientDeadline time.Duration
		wantLow        time.Duration
		wantHigh       time.Duration
	}{
		{
			name: "no timeout configured",
		},
		{
			name:     "request timeout applied",
			opts:     []Option{WithRequestTimeout(time.Hour)},
			wantLow:  59 * time.Minute,
			wantHigh: time.Hour,
		},
		{
			name:           "client deadline is kept",
			opts:           []Option{WithRequestTimeout(time.Minute)},
			clientDeadline: 2 * time.Hour,
			wantLow:        119 * time.Minute,
			wantHigh:       2 * time.Hour,
		},
		{
			name:     "method timeout overrides request timeout",
			opts:     []Option{WithRequestTimeout(time.Hour), WithMethodTimeout(methodUnaryCall, 30*time.Minute)},
			wantLow:  29 * time.Minute,
			wantHigh: 30 * time.Minute,
		},
		{
			name: "method timeout disabled",
			opts: []Option{WithRequestTimeout(time.Hour), WithMethodTimeout(methodUnaryCall, -1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := newTestServer(t, &testService{}, tt.opts...)

			ctx := t.Context()

			if tt.clientDeadline > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.clientDeadline)
				defer cancel()
			}

			resp, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
			require.NoError(t, err)

			if tt.wantHigh == 0 {
				require.Equal(t, noDeadline, resp.GetHostname())
				return
			}

			requireDeadline(t, resp.GetHostname(), tt.wantLow, tt.wantHigh)
		})
	}
}

func TestDeadline_stream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		opts           []Option
		clientDeadline time.Duration
		wantLow        time.Duration
		wantHigh       time.Duration
	}{
		{
			name: "request timeout does not apply to streams",
			opts: []Option{WithRequestTimeout(time.Hour)},
		},
		{
			name:     "method timeout applied",
			opts:     []Option{WithMethodTimeout(methodStreamingOutputCall, 30*time.Minute)},
			wantLow:  29 * time.Minute,
			wantHigh: 30 * time.Minute,
		},
		{
			name:           "client deadline is kept",
			opts:           []Option{WithMethodTimeout(methodStreamingOutputCall, time.Minute)},
			clientDeadline: 2 * time.Hour,
			wantLow:        119 * time.Minute,
			wantHigh:       2 * time.Hour,
		},
		{
			name: "method timeout disabled",
			opts: []Option{WithMethodTimeout(methodStreamingOutputCall, -1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := newTestServer(t, &testService{}, tt.opts...)

			ctx := t.Context()

			if tt.clientDeadline > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.clientDeadline)
				defer cancel()
			}

			stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{
				ResponseParameters: []*testgrpc.ResponseParameters{{}},
			})
			require.NoError(t, err)

			resp, err := stream.Recv()
			require.NoError(t, err)

			_, info, ok := strings.Cut(string(resp.GetPayload().GetBody()), "|")
			require.True(t, ok)

			if tt.wantHigh == 0 {
				require.Equal(t, noDeadline, info)
				return
			}

			requireDeadline(t, info, tt.wantLow, tt.wantHigh)
		})
	}
}

func TestCustomInterceptors(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []string
	)

	record := func(name string, ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()

		calls = append(calls, name+":"+traceid.FromContext(ctx, "missing"))
	}

	unary := func(name string) grpc.UnaryServerInterceptor {
		return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			record(name, ctx)
			return handler(ctx, req)
		}
	}

	stream := func(name string) grpc.StreamServerInterceptor {
		return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			record(name, ss.Context())
			return handler(srv, ss)
		}
	}

	_, client := newTestServer(t, &testService{},
		WithUnaryInterceptor(unary("u1"), unary("u2")),
		WithStreamInterceptor(stream("s1")),
		WithStreamInterceptor(stream("s2")),
	)

	_, err := client.UnaryCall(withTraceID(rpcContext(t), "id-1"), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	s, err := client.StreamingOutputCall(withTraceID(rpcContext(t), "id-2"), &testgrpc.StreamingOutputCallRequest{})
	require.NoError(t, err)

	_, err = s.Recv()
	require.ErrorIs(t, err, io.EOF)

	mu.Lock()
	defer mu.Unlock()

	require.Equal(t, []string{"u1:id-1", "u2:id-1", "s1:id-2", "s2:id-2"}, calls,
		"custom interceptors run in order, after the trace ID is injected")
}

func TestMessageSizeLimits(t *testing.T) {
	t.Parallel()

	payload := &testgrpc.Payload{Body: make([]byte, 1024)}

	tests := []struct {
		name string
		opt  Option
	}{
		{name: "max receive size", opt: WithMaxRecvMsgSize(64)},
		{name: "max send size", opt: WithMaxSendMsgSize(64)},
		{name: "raw server option", opt: WithServerOption(grpc.MaxRecvMsgSize(64))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := newTestServer(t, &testService{}, tt.opt)

			_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{Payload: payload})
			require.Equal(t, codes.ResourceExhausted, status.Code(err))

			_, err = client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
			require.NoError(t, err)
		})
	}
}

func TestPeerAddr(t *testing.T) {
	t.Parallel()

	require.Empty(t, peerAddr(t.Context()))
	require.Empty(t, peerAddr(peer.NewContext(t.Context(), &peer.Peer{})))

	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234}
	require.Equal(t, "10.0.0.1:1234", peerAddr(peer.NewContext(t.Context(), &peer.Peer{Addr: addr})))
}

func TestUserAgent(t *testing.T) {
	t.Parallel()

	require.Empty(t, userAgent(t.Context()))
	require.Empty(t, userAgent(metadata.NewIncomingContext(t.Context(), metadata.Pairs("other", "x"))))
	require.Equal(t, "agent/1", userAgent(metadata.NewIncomingContext(t.Context(), metadata.Pairs("user-agent", "agent/1"))))
}

func TestInterceptors_outsideRPC(t *testing.T) {
	t.Parallel()

	// Interceptors must not fail when the context carries no gRPC transport
	// stream (grpc.SetHeader then returns an error that is ignored).
	cfg := defaultConfig()
	cfg.logger = discardLogger()

	info := &grpc.UnaryServerInfo{FullMethod: methodUnaryCall}

	resp, err := cfg.unaryRequestInterceptor(t.Context(), "req", info, func(ctx context.Context, _ any) (any, error) {
		return traceid.FromContext(ctx, ""), nil
	})
	require.NoError(t, err)
	require.Len(t, resp, 36)

	errHandler := errors.New("handler failure")

	_, err = cfg.unaryRequestInterceptor(t.Context(), "req", info, func(_ context.Context, _ any) (any, error) {
		return nil, errHandler
	})
	require.ErrorIs(t, err, errHandler)
}
