package grpcclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// bufTarget is the target of connections to a bufconn listener.
const bufTarget = "passthrough:///bufnet"

// noDeadline is reported by the test service when the RPC context has no deadline.
const noDeadline = "none"

// Full method names of the test service.
const (
	methodUnaryCall = "/grpc.testing.TestService/UnaryCall"
	methodEmptyCall = "/grpc.testing.TestService/EmptyCall"
)

// testService reports what its handlers observe: Username carries the
// incoming trace ID and Hostname the remaining deadline (or noDeadline).
// UnaryCall and EmptyCall fail with failCode while fewer than failFirst calls
// have been made.
type testService struct {
	testgrpc.UnimplementedTestServiceServer

	failFirst int64
	failCode  codes.Code
	calls     atomic.Int64
}

func (s *testService) EmptyCall(_ context.Context, _ *testgrpc.Empty) (*testgrpc.Empty, error) {
	err := s.fail()
	if err != nil {
		return nil, err
	}

	return &testgrpc.Empty{}, nil
}

func (s *testService) UnaryCall(ctx context.Context, req *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	err := s.fail()
	if err != nil {
		return nil, err
	}

	err = echoStatus(req.GetResponseStatus())
	if err != nil {
		return nil, err
	}

	return &testgrpc.SimpleResponse{
		Payload:  req.GetPayload(),
		Username: incomingTraceID(ctx),
		Hostname: deadlineInfo(ctx),
	}, nil
}

//nolint:wrapcheck // gRPC handlers return status errors as-is
func (s *testService) StreamingOutputCall(
	req *testgrpc.StreamingOutputCallRequest,
	stream grpc.ServerStreamingServer[testgrpc.StreamingOutputCallResponse],
) error {
	body := incomingTraceID(stream.Context()) + "|" + deadlineInfo(stream.Context())

	for range req.GetResponseParameters() {
		err := stream.Send(&testgrpc.StreamingOutputCallResponse{Payload: &testgrpc.Payload{Body: []byte(body)}})
		if err != nil {
			return err
		}
	}

	return echoStatus(req.GetResponseStatus())
}

//nolint:wrapcheck // gRPC handlers return status errors as-is
func (s *testService) StreamingInputCall(
	stream grpc.ClientStreamingServer[testgrpc.StreamingInputCallRequest, testgrpc.StreamingInputCallResponse],
) error {
	var n int32

	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&testgrpc.StreamingInputCallResponse{AggregatedPayloadSize: n})
		}

		if err != nil {
			return err
		}

		n++
	}
}

//nolint:wrapcheck // gRPC handlers return status errors as-is
func (s *testService) FullDuplexCall(
	stream grpc.BidiStreamingServer[testgrpc.StreamingOutputCallRequest, testgrpc.StreamingOutputCallResponse],
) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		err = stream.Send(&testgrpc.StreamingOutputCallResponse{Payload: req.GetPayload()})
		if err != nil {
			return err
		}
	}
}

// fail returns the configured error while fewer than failFirst calls have
// been made.
func (s *testService) fail() error {
	if s.calls.Add(1) <= s.failFirst {
		return status.Error(s.failCode, "induced failure") //nolint:wrapcheck
	}

	return nil
}

// echoStatus returns the requested status error, or nil for a missing or OK status.
func echoStatus(st *testgrpc.EchoStatus) error {
	if st.GetCode() == 0 {
		return nil
	}

	return status.Error(codes.Code(st.GetCode()), st.GetMessage()) //nolint:wrapcheck
}

// incomingTraceID returns the first trace ID value of the incoming metadata.
func incomingTraceID(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)

	vals := md.Get(grpcutil.DefaultTraceIDMetadataKey)
	if len(vals) == 0 {
		return ""
	}

	return vals[0]
}

// deadlineInfo returns the time left before the context deadline, or noDeadline.
func deadlineInfo(ctx context.Context) string {
	d, ok := ctx.Deadline()
	if !ok {
		return noDeadline
	}

	return time.Until(d).String()
}

// requireDeadline asserts that info reports a deadline in (low, high].
func requireDeadline(t *testing.T, info string, low, high time.Duration) {
	t.Helper()

	d, err := time.ParseDuration(info)
	require.NoError(t, err, "expected a deadline, got %q", info)
	require.Greater(t, d, low)
	require.LessOrEqual(t, d, high)
}

// discardLogger returns a logger that drops every entry.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// logSink collects JSON log lines written concurrently.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p) //nolint:wrapcheck
}

// entries returns the decoded log entries with the given message.
func (l *logSink) entries(t *testing.T, msg string) []map[string]any {
	t.Helper()

	l.mu.Lock()
	data := bytes.Clone(l.buf.Bytes())
	l.mu.Unlock()

	var out []map[string]any

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var e map[string]any

		require.NoError(t, json.Unmarshal(sc.Bytes(), &e))

		if e[slog.MessageKey] == msg {
			out = append(out, e)
		}
	}

	require.NoError(t, sc.Err())

	return out
}

// waitEntry waits until the sink holds an outbound entry, checks that it is
// the only one, and returns it.
func (l *logSink) waitEntry(t *testing.T) map[string]any {
	t.Helper()

	var out []map[string]any

	require.Eventually(t, func() bool {
		out = l.entries(t, logMessage)
		return len(out) > 0
	}, 10*time.Second, 5*time.Millisecond)

	require.Len(t, out, 1)

	return out[0]
}

// newTestLogger returns a JSON logger at level that writes to a logSink.
func newTestLogger(level slog.Level) (*slog.Logger, *logSink) {
	sink := &logSink{}
	return slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: level})), sink
}

// newBufServer serves svc on an in-memory listener and returns the dial option
// that connects to it.
func newBufServer(t *testing.T, svc testgrpc.TestServiceServer, opts ...grpc.ServerOption) grpc.DialOption {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
	testgrpc.RegisterTestServiceServer(srv, svc)

	go func() { _ = srv.Serve(lis) }()

	t.Cleanup(srv.Stop)

	return grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	})
}

// newTestConn returns a plaintext connection to the in-memory server built
// with New. Options are applied after a discarding logger, so they can
// override it.
func newTestConn(t *testing.T, dialer grpc.DialOption, opts ...Option) *grpc.ClientConn {
	t.Helper()

	base := []Option{WithInsecure(), WithLogger(discardLogger()), WithDialOption(dialer)}

	conn, err := New(bufTarget, append(base, opts...)...)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// newTestClient returns a test service client connected to a new server for
// svc.
func newTestClient(t *testing.T, svc testgrpc.TestServiceServer, opts ...Option) testgrpc.TestServiceClient {
	t.Helper()

	return testgrpc.NewTestServiceClient(newTestConn(t, newBufServer(t, svc), opts...))
}

// rpcContext returns a context bounded for a single test RPC.
func rpcContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// makeTestCert generates an ephemeral self-signed ECDSA certificate for
// "localhost" and returns it with a pool that trusts it.
func makeTestCert(t *testing.T) (*x509.CertPool, tls.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return pool, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
