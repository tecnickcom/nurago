package grpcutil_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func ExampleOutgoingContextWithTraceID() {
	// The trace ID stored in the request context, for example by an inbound
	// server interceptor.
	ctx := traceid.NewContext(context.Background(), "019a1d4e-7b3c-7f00-8000-000000000001")

	// Copy it into the outgoing metadata before calling a downstream service.
	ctx, id := grpcutil.OutgoingContextWithTraceID(ctx, grpcutil.DefaultTraceIDMetadataKey, traceid.DefaultValue)

	md, _ := metadata.FromOutgoingContext(ctx)

	fmt.Println(id)
	fmt.Println(md.Get(grpcutil.DefaultTraceIDMetadataKey)[0])

	// Output:
	// 019a1d4e-7b3c-7f00-8000-000000000001
	// 019a1d4e-7b3c-7f00-8000-000000000001
}

func ExampleTraceIDFromIncomingContext() {
	// Incoming metadata as received by a server handler.
	md := metadata.Pairs(grpcutil.DefaultTraceIDMetadataKey, "abc-123")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	id := grpcutil.TraceIDFromIncomingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, traceid.DefaultValue)

	fmt.Println(id)

	// Output:
	// abc-123
}

func ExampleCodeFromHTTPStatus() {
	fmt.Println(grpcutil.CodeFromHTTPStatus(http.StatusNotFound))
	fmt.Println(grpcutil.CodeFromHTTPStatus(http.StatusUnprocessableEntity))
	fmt.Println(grpcutil.HTTPStatusFromCode(codes.Unavailable))

	// Output:
	// NotFound
	// InvalidArgument
	// 503
}
