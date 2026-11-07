package grpcclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
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
	"google.golang.org/grpc/status"
)

func TestTraceID_unary(t *testing.T) {
	t.Parallel()

	var calls, mismatches atomic.Int64

	// Checks that the inner interceptors see the propagated trace ID in the context.
	check := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		calls.Add(1)

		md := metadata.ValueFromOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey)
		if len(md) != 1 || md[0] != traceid.FromContext(ctx, "") {
			mismatches.Add(1)
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}

	client := newTestClient(t, &testService{}, WithUnaryInterceptor(check))

	t.Cleanup(func() {
		require.EqualValues(t, 6, calls.Load())
		require.Zero(t, mismatches.Load())
	})

	tests := []struct {
		name  string
		ctx   func(ctx context.Context) context.Context
		want  string
		check func(t *testing.T, got string)
	}{
		{
			name: "from context",
			ctx:  func(ctx context.Context) context.Context { return traceid.NewContext(ctx, "ctx-id") },
			want: "ctx-id",
		},
		{
			name: "context replaces metadata",
			ctx: func(ctx context.Context) context.Context {
				ctx = metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, "md-id")
				return traceid.NewContext(ctx, "ctx-id")
			},
			want: "ctx-id",
		},
		{
			name: "from metadata when the context has none",
			ctx: func(ctx context.Context) context.Context {
				return metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, "md-id")
			},
			want: "md-id",
		},
		{
			name: "from metadata when the context ID is invalid",
			ctx: func(ctx context.Context) context.Context {
				ctx = metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, "md-id")
				return traceid.NewContext(ctx, "bad id")
			},
			want: "md-id",
		},
		{
			name: "generated when missing",
			ctx:  func(ctx context.Context) context.Context { return ctx },
		},
		{
			name: "generated when both are invalid",
			ctx: func(ctx context.Context) context.Context {
				ctx = metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, "bad md")
				return traceid.NewContext(ctx, "bad id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := client.UnaryCall(tt.ctx(rpcContext(t)), &testgrpc.SimpleRequest{})
			require.NoError(t, err)

			got := resp.GetUsername()
			if tt.want != "" {
				require.Equal(t, tt.want, got)
			} else {
				require.Len(t, got, 36)
				require.True(t, traceid.Valid(got))
			}
		})
	}
}

func TestTraceID_stream(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, &testService{})

	ctx := traceid.NewContext(rpcContext(t), "stream-id")

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{
		ResponseParameters: []*testgrpc.ResponseParameters{{}},
	})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(resp.GetPayload().GetBody()), "stream-id|"))
}

func TestTraceID_customMetadataKey(t *testing.T) {
	t.Parallel()

	got := make(chan []string, 1)

	srvDialer := newBufServer(t, &testService{}, grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			got <- md.Get("x-trace-id")

			return handler(ctx, req)
		},
	))

	client := testgrpc.NewTestServiceClient(newTestConn(t, srvDialer, WithTraceIDMetadataKey("X-Trace-Id")))

	_, err := client.UnaryCall(traceid.NewContext(rpcContext(t), "custom-123"), &testgrpc.SimpleRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"custom-123"}, <-got)
}

func TestTraceContext_unchangedMetadata(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	ctx := traceid.NewContext(t.Context(), "abc-123")
	ctx = metadata.AppendToOutgoingContext(ctx, cfg.traceIDMetadataKey, "abc-123")
	require.Equal(t, ctx, cfg.traceContext(ctx), "ID already set")

	ctx = metadata.AppendToOutgoingContext(ctx, cfg.traceIDMetadataKey, "def-456")
	got := cfg.traceContext(ctx)
	require.NotEqual(t, ctx, got, "second value under the key")

	md, _ := metadata.FromOutgoingContext(got)
	require.Equal(t, []string{"abc-123"}, md.Get(cfg.traceIDMetadataKey))
}

func FuzzTraceContext(f *testing.F) {
	f.Add("", "")
	f.Add("ctx-id", "md-id")
	f.Add("bad id", "md-id")
	f.Add("ctx-id", "bad\r\nid")
	f.Add(strings.Repeat("a", traceid.MaxIDLen+1), "")

	cfg := defaultConfig()

	f.Fuzz(func(t *testing.T, ctxID, mdID string) {
		ctx := traceid.NewContext(t.Context(), ctxID)
		ctx = metadata.AppendToOutgoingContext(ctx, cfg.traceIDMetadataKey, mdID)

		ctx = cfg.traceContext(ctx)

		id := traceid.FromContext(ctx, "")
		require.True(t, traceid.Valid(id))

		md, _ := metadata.FromOutgoingContext(ctx)
		require.Equal(t, []string{id}, md.Get(cfg.traceIDMetadataKey))

		switch {
		case traceid.Valid(ctxID):
			require.Equal(t, ctxID, id)
		case traceid.Valid(mdID):
			require.Equal(t, mdID, id)
		}
	})
}

func TestDeadline(t *testing.T) {
	t.Parallel()

	t.Run("default applied", func(t *testing.T) {
		t.Parallel()

		client := newTestClient(t, &testService{})

		resp, err := client.UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		requireDeadline(t, resp.GetHostname(), 50*time.Second, time.Minute)
	})

	t.Run("custom applied", func(t *testing.T) {
		t.Parallel()

		client := newTestClient(t, &testService{}, WithTimeout(5*time.Second))

		resp, err := client.UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		requireDeadline(t, resp.GetHostname(), time.Second, 5*time.Second)
	})

	t.Run("caller deadline kept", func(t *testing.T) {
		t.Parallel()

		client := newTestClient(t, &testService{}, WithTimeout(5*time.Second))

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()

		resp, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		requireDeadline(t, resp.GetHostname(), 20*time.Second, 30*time.Second)
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		client := newTestClient(t, &testService{}, WithTimeout(0))

		resp, err := client.UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.Equal(t, noDeadline, resp.GetHostname())
	})

	t.Run("streams exempt", func(t *testing.T) {
		t.Parallel()

		client := newTestClient(t, &testService{}, WithTimeout(5*time.Second))

		stream, err := client.StreamingOutputCall(t.Context(), &testgrpc.StreamingOutputCallRequest{
			ResponseParameters: []*testgrpc.ResponseParameters{{}},
		})
		require.NoError(t, err)

		resp, err := stream.Recv()
		require.NoError(t, err)
		require.True(t, strings.HasSuffix(string(resp.GetPayload().GetBody()), "|"+noDeadline))
	})
}

func TestLogging_unary(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)
	client := newTestClient(t, &testService{}, WithLogger(logger), WithComponent("test-client"))

	ctx := traceid.NewContext(rpcContext(t), "log-id")

	_, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	e := sink.waitEntry(t)

	require.Equal(t, slog.LevelInfo.String(), e[slog.LevelKey])
	require.Equal(t, "test-client", e["component"])
	require.Equal(t, "log-id", e[traceid.DefaultLogKey])
	require.Equal(t, bufTarget, e["grpc_target"])
	require.Equal(t, "grpc.testing.TestService", e["grpc_service"])
	require.Equal(t, "UnaryCall", e["grpc_method"])
	require.Equal(t, rpcTypeUnary, e["grpc_type"])
	require.Equal(t, codes.OK.String(), e["grpc_code"])
	require.Contains(t, e, "request_time")
	require.Contains(t, e, "response_time")
	require.Contains(t, e, "response_duration")
	require.NotContains(t, e, "error")
	require.NotContains(t, e, "request")
	require.NotContains(t, e, "response")
}

func TestLogging_levels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		code      codes.Code
		wantLevel slog.Level
	}{
		{name: "client error", code: codes.NotFound, wantLevel: slog.LevelInfo},
		{name: "canceled", code: codes.Canceled, wantLevel: slog.LevelInfo},
		{name: "server error", code: codes.Internal, wantLevel: slog.LevelError},
		{name: "unavailable", code: codes.Unavailable, wantLevel: slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, sink := newTestLogger(slog.LevelInfo)
			client := newTestClient(t, &testService{}, WithLogger(logger))

			_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{
				ResponseStatus: &testgrpc.EchoStatus{Code: int32(tt.code), Message: "secret=hunter2"},
			})
			require.Equal(t, tt.code, status.Code(err))

			e := sink.waitEntry(t)
			require.Equal(t, tt.wantLevel.String(), e[slog.LevelKey])
			require.Equal(t, tt.code.String(), e["grpc_code"])
			require.Contains(t, e["error"], "rpc error")
		})
	}
}

func TestLogging_redactsError(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)
	client := newTestClient(t, &testService{}, WithLogger(logger), WithRedactFn(func(_ []byte) string { return "REDACTED" }))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound), Message: "token=abc"},
	})
	require.Error(t, err)

	e := sink.waitEntry(t)
	require.Equal(t, "REDACTED", e["error"])
}

func TestLogging_prefix(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)
	client := newTestClient(t, &testService{}, WithLogger(logger), WithLogPrefix("out_"))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	e := sink.waitEntry(t)
	require.Equal(t, "-", e["out_component"])
	require.Equal(t, "UnaryCall", e["out_grpc_method"])
	require.Contains(t, e, "out_"+traceid.DefaultLogKey)
	require.NotContains(t, e, "grpc_method")
}

func TestLogging_debugDumps(t *testing.T) {
	t.Parallel()

	t.Run("redacted dumps", func(t *testing.T) {
		t.Parallel()

		logger, sink := newTestLogger(slog.LevelDebug)
		client := newTestClient(t, &testService{}, WithLogger(logger),
			WithRedactFn(func(b []byte) string { return strings.ReplaceAll(string(b), "secret", "*") }))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{Payload: &testgrpc.Payload{Body: []byte("x")}, FillUsername: true})
		require.NoError(t, err)

		e := sink.waitEntry(t)
		require.Equal(t, slog.LevelDebug.String(), e[slog.LevelKey])
		reqDump, _ := e["request"].(string)
		require.JSONEq(t, `{"payload":{"body":"eA=="},"fillUsername":true}`, reqDump)
		require.Contains(t, e["response"], `"payload":{"body":"eA=="}`)
	})

	t.Run("capped", func(t *testing.T) {
		t.Parallel()

		logger, sink := newTestLogger(slog.LevelDebug)
		client := newTestClient(t, &testService{}, WithLogger(logger), WithMaxDumpSize(8))

		req := &testgrpc.SimpleRequest{Payload: &testgrpc.Payload{Body: []byte("0123456789")}}

		_, err := client.UnaryCall(rpcContext(t), req)
		require.NoError(t, err)

		e := sink.waitEntry(t)
		require.Contains(t, e["request"], "[omitted:")
		require.Contains(t, e["request"], "exceeds the dump cap of 8 bytes")
	})

	t.Run("no response dump on error", func(t *testing.T) {
		t.Parallel()

		logger, sink := newTestLogger(slog.LevelDebug)
		client := newTestClient(t, &testService{}, WithLogger(logger))

		_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{
			ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound)},
		})
		require.Error(t, err)

		e := sink.waitEntry(t)
		require.Equal(t, slog.LevelDebug.String(), e[slog.LevelKey])
		require.Contains(t, e, "request")
		require.NotContains(t, e, "response")
	})
}

func TestLogging_streams(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T) (testgrpc.TestServiceClient, *logSink) {
		t.Helper()

		logger, sink := newTestLogger(slog.LevelInfo)

		return newTestClient(t, &testService{}, WithLogger(logger)), sink
	}

	t.Run("server stream", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		stream, err := client.StreamingOutputCall(rpcContext(t), &testgrpc.StreamingOutputCallRequest{
			ResponseParameters: []*testgrpc.ResponseParameters{{}, {}, {}},
		})
		require.NoError(t, err)

		for {
			_, err = stream.Recv()
			if err != nil {
				break
			}
		}

		require.ErrorIs(t, err, io.EOF)

		e := sink.waitEntry(t)
		require.Equal(t, rpcTypeServerStream, e["grpc_type"])
		require.Equal(t, codes.OK.String(), e["grpc_code"])
		require.InDelta(t, 1, e["request_messages"], 0)
		require.InDelta(t, 3, e["response_messages"], 0)
	})

	t.Run("server stream error", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		stream, err := client.StreamingOutputCall(rpcContext(t), &testgrpc.StreamingOutputCallRequest{
			ResponseParameters: []*testgrpc.ResponseParameters{{}},
			ResponseStatus:     &testgrpc.EchoStatus{Code: int32(codes.DataLoss)},
		})
		require.NoError(t, err)

		_, err = stream.Recv()
		require.NoError(t, err)

		_, err = stream.Recv()
		require.Equal(t, codes.DataLoss, status.Code(err))

		e := sink.waitEntry(t)
		require.Equal(t, slog.LevelError.String(), e[slog.LevelKey])
		require.Equal(t, codes.DataLoss.String(), e["grpc_code"])
		require.InDelta(t, 1, e["response_messages"], 0)
	})

	t.Run("client stream", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		stream, err := client.StreamingInputCall(rpcContext(t))
		require.NoError(t, err)

		for range 4 {
			require.NoError(t, stream.Send(&testgrpc.StreamingInputCallRequest{}))
		}

		resp, err := stream.CloseAndRecv()
		require.NoError(t, err)
		require.EqualValues(t, 4, resp.GetAggregatedPayloadSize())

		e := sink.waitEntry(t)
		require.Equal(t, rpcTypeClientStream, e["grpc_type"])
		require.Equal(t, codes.OK.String(), e["grpc_code"])
		require.InDelta(t, 4, e["request_messages"], 0)
		require.InDelta(t, 1, e["response_messages"], 0)
	})

	t.Run("bidi stream", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		stream, err := client.FullDuplexCall(rpcContext(t))
		require.NoError(t, err)

		for range 2 {
			require.NoError(t, stream.Send(&testgrpc.StreamingOutputCallRequest{}))

			_, err = stream.Recv()
			require.NoError(t, err)
		}

		require.NoError(t, stream.CloseSend())

		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF)

		e := sink.waitEntry(t)
		require.Equal(t, rpcTypeBidiStream, e["grpc_type"])
		require.InDelta(t, 2, e["request_messages"], 0)
		require.InDelta(t, 2, e["response_messages"], 0)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		ctx, cancel := context.WithCancel(rpcContext(t))

		stream, err := client.FullDuplexCall(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&testgrpc.StreamingOutputCallRequest{}))

		cancel()

		e := sink.waitEntry(t)
		require.Equal(t, slog.LevelInfo.String(), e[slog.LevelKey])
		require.Equal(t, codes.Canceled.String(), e["grpc_code"])

		// A later receive error does not log again.
		_, err = stream.Recv()
		require.Equal(t, codes.Canceled, status.Code(err))

		time.Sleep(20 * time.Millisecond)
		require.Len(t, sink.entries(t, logMessage), 1)
	})

	t.Run("creation error", func(t *testing.T) {
		t.Parallel()

		client, sink := newClient(t)

		ctx, cancel := context.WithCancel(rpcContext(t))
		cancel()

		_, err := client.FullDuplexCall(ctx)
		require.Equal(t, codes.Canceled, status.Code(err))

		e := sink.waitEntry(t)
		require.Equal(t, codes.Canceled.String(), e["grpc_code"])
		require.InDelta(t, 0, e["request_messages"], 0)
	})
}

func TestLogging_streamSendError(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)
	client := newTestClient(t, &testService{}, WithLogger(logger), WithMaxSendMsgSize(4))

	stream, err := client.FullDuplexCall(rpcContext(t))
	require.NoError(t, err)

	err = stream.Send(&testgrpc.StreamingOutputCallRequest{Payload: &testgrpc.Payload{Body: []byte("0123456789")}})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	e := sink.waitEntry(t)
	require.Equal(t, codes.ResourceExhausted.String(), e["grpc_code"])
	require.InDelta(t, 0, e["request_messages"], 0)
}

func TestLogAttrs_nilConn(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	attrs := cfg.logAttrs(t.Context(), nil, "bad", rpcTypeUnary, time.Now(), errors.New("plain"))

	got := make(map[string]string, len(attrs))
	for _, a := range attrs {
		got[a.Key] = a.Value.String()
	}

	require.Empty(t, got["grpc_target"])
	require.Empty(t, got["grpc_service"])
	require.Equal(t, "bad", got["grpc_method"])
	require.Equal(t, codes.Unknown.String(), got["grpc_code"])
	require.Equal(t, "plain", got["error"])
}

func TestStreamType(t *testing.T) {
	t.Parallel()

	require.Equal(t, rpcTypeBidiStream, streamType(&grpc.StreamDesc{ClientStreams: true, ServerStreams: true}))
	require.Equal(t, rpcTypeClientStream, streamType(&grpc.StreamDesc{ClientStreams: true}))
	require.Equal(t, rpcTypeServerStream, streamType(&grpc.StreamDesc{ServerStreams: true}))
}
