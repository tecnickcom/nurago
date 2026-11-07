package grpcserver_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/jwt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// greeter answers with the subject of the verified JWT claims.
type greeter struct {
	testgrpc.UnimplementedTestServiceServer
}

func (g *greeter) UnaryCall(ctx context.Context, _ *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	claims, ok := jwt.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	return &testgrpc.SimpleResponse{Username: "hello " + claims.Subject}, nil
}

// greeterBinder registers the greeter service.
type greeterBinder struct{}

func (b *greeterBinder) BindGRPC(_ context.Context, s grpc.ServiceRegistrar) error {
	testgrpc.RegisterTestServiceServer(s, &greeter{})
	return nil
}

// jwtPublicMethod reports whether fullMethod is allowed without a token: the
// health methods called by load balancers and probes. List is excluded because
// it reports every check ID.
func jwtPublicMethod(fullMethod string) bool {
	return fullMethod == healthpb.Health_Check_FullMethodName ||
		fullMethod == healthpb.Health_Watch_FullMethodName
}

// jwtAuthenticate validates the bearer token in the "authorization" metadata
// of ctx and returns ctx with its claims.
func jwtAuthenticate(ctx context.Context, auth *jwt.JWT) (context.Context, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	token, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	claims, err := auth.VerifyToken(token)
	if err != nil {
		// The verification error is not returned to the client.
		return nil, status.Error(codes.Unauthenticated, "invalid bearer token") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	return jwt.NewContextWithClaims(ctx, claims), nil
}

// jwtUnaryInterceptor authenticates the unary RPCs, except the public ones.
func jwtUnaryInterceptor(auth *jwt.JWT) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if jwtPublicMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		ctx, err := jwtAuthenticate(ctx, auth)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// jwtStream is a grpc.ServerStream with the context of the verified claims.
type jwtStream struct {
	grpc.ServerStream

	ctx context.Context //nolint:containedctx // the stream context carrying the claims
}

func (s *jwtStream) Context() context.Context {
	return s.ctx
}

// jwtStreamInterceptor authenticates the streaming RPCs, except the public
// ones.
func jwtStreamInterceptor(auth *jwt.JWT) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if jwtPublicMethod(info.FullMethod) {
			return handler(srv, ss)
		}

		ctx, err := jwtAuthenticate(ss.Context(), auth)
		if err != nil {
			return err
		}

		return handler(srv, &jwtStream{ServerStream: ss, ctx: ctx})
	}
}

// Example_jwtAuth protects the RPCs of a server with a JWT bearer token sent
// in the request metadata.
func Example_jwtAuth() {
	ctx := context.Background()

	auth, err := jwt.New(
		[]byte("0123456789abcdef0123456789abcdef"),
		func(_, _ string) (bool, error) { return false, nil },
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv, err := grpcserver.New(
		ctx,
		&greeterBinder{},
		grpcserver.WithServerAddr("127.0.0.1:0"),
		grpcserver.WithLogger(slog.New(slog.DiscardHandler)),
		grpcserver.WithHealthService(nil),
		grpcserver.WithUnaryInterceptor(jwtUnaryInterceptor(auth)),
		grpcserver.WithStreamInterceptor(jwtStreamInterceptor(auth)),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv.StartServer()

	defer func() { _ = srv.Shutdown(ctx) }()

	conn, err := grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Println(err)
		return
	}

	defer func() { _ = conn.Close() }()

	client := testgrpc.NewTestServiceClient(conn)

	_, err = client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	fmt.Println(status.Code(err))

	// The identity is verified by other means; IssueToken only mints the token.
	token, err := auth.IssueToken("alice")
	if err != nil {
		fmt.Println(err)
		return
	}

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)

	resp, err := client.UnaryCall(authCtx, &testgrpc.SimpleRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(resp.GetUsername())

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{})
	if err == nil {
		_, err = stream.Recv()
	}

	fmt.Println(status.Code(err))

	healthClient := healthpb.NewHealthClient(conn)

	health, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(health.GetStatus())

	_, err = healthClient.List(ctx, &healthpb.HealthListRequest{})
	fmt.Println(status.Code(err))

	// Output:
	// Unauthenticated
	// hello alice
	// Unauthenticated
	// SERVING
	// Unauthenticated
}
