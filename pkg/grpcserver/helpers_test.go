package grpcserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
)

// panicCode is an EchoStatus code that makes the test service panic.
const panicCode = 99

// noDeadline is reported by the test service when the RPC context has no deadline.
const noDeadline = "none"

// Full method names of the test service.
const (
	methodUnaryCall           = "/grpc.testing.TestService/UnaryCall"
	methodStreamingOutputCall = "/grpc.testing.TestService/StreamingOutputCall"
	methodStreamingInputCall  = "/grpc.testing.TestService/StreamingInputCall"
	methodFullDuplexCall      = "/grpc.testing.TestService/FullDuplexCall"
)

// baseCtxKey is a context key used to verify that RPC contexts expose the
// values of the context passed to New.
type baseCtxKey struct{}

// testService reports what its handlers observe in the response fields:
// Username carries the trace ID, Hostname the remaining deadline (or
// noDeadline), and ServerId the base context value.
type testService struct {
	testgrpc.UnimplementedTestServiceServer

	block   chan struct{} // when set, FullDuplexCall blocks on it, ignoring cancelation
	started chan struct{} // when set, FullDuplexCall signals it before blocking
}

func (s *testService) UnaryCall(ctx context.Context, req *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	err := echoStatus(req.GetResponseStatus())
	if err != nil {
		return nil, err
	}

	return &testgrpc.SimpleResponse{
		Payload:  req.GetPayload(),
		Username: traceid.FromContext(ctx, ""),
		Hostname: deadlineInfo(ctx),
		ServerId: baseValue(ctx),
	}, nil
}

//nolint:wrapcheck // gRPC handlers return status errors as-is
func (s *testService) StreamingOutputCall(
	req *testgrpc.StreamingOutputCallRequest,
	stream grpc.ServerStreamingServer[testgrpc.StreamingOutputCallResponse],
) error {
	err := echoStatus(req.GetResponseStatus())
	if err != nil {
		return err
	}

	body := traceid.FromContext(stream.Context(), "") + "|" + deadlineInfo(stream.Context())

	for range req.GetResponseParameters() {
		err := stream.Send(&testgrpc.StreamingOutputCallResponse{Payload: &testgrpc.Payload{Body: []byte(body)}})
		if err != nil {
			return err
		}
	}

	return nil
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
	if s.block != nil {
		s.started <- struct{}{}

		<-s.block

		return nil
	}

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		err = echoStatus(req.GetResponseStatus())
		if err != nil {
			return err
		}

		err = stream.Send(&testgrpc.StreamingOutputCallResponse{Payload: req.GetPayload()})
		if err != nil {
			return err
		}
	}
}

// echoStatus returns the requested status error, panics for panicCode, and
// returns nil for a missing or OK status.
//
//nolint:wrapcheck // gRPC handlers return status errors as-is
func echoStatus(st *testgrpc.EchoStatus) error {
	if st.GetCode() == 0 {
		return nil
	}

	if st.GetCode() == panicCode {
		panic("test panic")
	}

	return status.Error(codes.Code(st.GetCode()), st.GetMessage())
}

// deadlineInfo returns the time left before the context deadline, or noDeadline.
func deadlineInfo(ctx context.Context) string {
	d, ok := ctx.Deadline()
	if !ok {
		return noDeadline
	}

	return time.Until(d).String()
}

// baseValue returns the baseCtxKey value of ctx, or an empty string.
func baseValue(ctx context.Context) string {
	v, _ := ctx.Value(baseCtxKey{}).(string)
	return v
}

// requireDeadline asserts that info reports a deadline in (low, high].
func requireDeadline(t *testing.T, info string, low, high time.Duration) {
	t.Helper()

	d, err := time.ParseDuration(info)
	require.NoError(t, err, "expected a deadline, got %q", info)
	require.Greater(t, d, low)
	require.LessOrEqual(t, d, high)
}

// serviceBinder registers a test service.
type serviceBinder struct {
	svc testgrpc.TestServiceServer
}

func (b *serviceBinder) BindGRPC(_ context.Context, s grpc.ServiceRegistrar) error {
	testgrpc.RegisterTestServiceServer(s, b.svc)
	return nil
}

// funcBinder adapts a function to the Binder interface.
type funcBinder func(ctx context.Context, s grpc.ServiceRegistrar) error

func (f funcBinder) BindGRPC(ctx context.Context, s grpc.ServiceRegistrar) error {
	return f(ctx, s)
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

// newTestLogger returns a JSON logger at level that writes to a logSink.
func newTestLogger(level slog.Level) (*slog.Logger, *logSink) {
	sink := &logSink{}
	return slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: level})), sink
}

// newTestServer starts a server bound to an ephemeral loopback port with the
// given test service and returns it with a connected client. Options are
// applied after a discarding logger and the address, so they can override
// both.
func newTestServer(t *testing.T, svc *testService, opts ...Option) (*GRPCServer, testgrpc.TestServiceClient) {
	t.Helper()

	return newTestServerCtx(t, t.Context(), svc, opts...)
}

// newTestServerCtx is newTestServer with an explicit New context.
func newTestServerCtx(t *testing.T, ctx context.Context, svc *testService, opts ...Option) (*GRPCServer, testgrpc.TestServiceClient) {
	t.Helper()

	base := []Option{WithLogger(discardLogger()), WithServerAddr("127.0.0.1:0")}

	srv, err := New(ctx, &serviceBinder{svc: svc}, append(base, opts...)...)
	require.NoError(t, err)

	srv.StartServer()

	// t.Context() is already canceled when cleanup runs.
	t.Cleanup(func() { _ = srv.Shutdown(context.WithoutCancel(ctx)) })

	return srv, newTestClient(t, srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// newTestClient returns a test service client for addr.
func newTestClient(t *testing.T, addr string, opts ...grpc.DialOption) testgrpc.TestServiceClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, opts...)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return testgrpc.NewTestServiceClient(conn)
}

// rpcContext returns a context bounded for a single test RPC.
func rpcContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// waitTimeout waits for wg and fails the test if it does not complete in time.
func waitTimeout(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for the wait group")
	}
}

// makeTestCert generates an ephemeral self-signed ECDSA certificate for
// 127.0.0.1 and returns the PEM-encoded certificate and key.
func makeTestCert(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}
