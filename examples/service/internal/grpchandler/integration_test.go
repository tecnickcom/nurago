//go:build integration

package grpchandler

// Integration tests against a running service, executed in the docker-compose
// integration environment by "make grpcapitest". The server addresses are read
// from the environment:
//   - NURAGOEXAMPLE_GRPC_ADDR: gRPC server address (host:port).
//   - NURAGOEXAMPLE_PUBLIC_URL: public HTTP server URL.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	itemv1 "github.com/nuragoexampleowner/nuragoexample/internal/gen/item/v1"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// unknownItemID is a valid UUIDv7 that matches no item.
const unknownItemID = "01900000-0000-7000-8000-000000000000"

// itemNameSeq makes the names created by one test run unique.
var itemNameSeq atomic.Int64 //nolint:gochecknoglobals

// restItem is the JSON representation of an item in the REST API.
type restItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Quantity uint32 `json:"quantity"`
}

// integrationEnv returns the gRPC address and the public HTTP URL.
func integrationEnv(t *testing.T) (string, string) {
	t.Helper()

	grpcAddr := os.Getenv("NURAGOEXAMPLE_GRPC_ADDR")
	publicURL := os.Getenv("NURAGOEXAMPLE_PUBLIC_URL")

	require.NotEmpty(t, grpcAddr, "NURAGOEXAMPLE_GRPC_ADDR is not set")
	require.NotEmpty(t, publicURL, "NURAGOEXAMPLE_PUBLIC_URL is not set")

	return grpcAddr, strings.TrimSuffix(publicURL, "/")
}

// newIntegrationConn connects to the gRPC server. Only the read RPCs are
// retried: CreateItem is not idempotent.
func newIntegrationConn(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()

	conn, err := grpcclient.New(
		addr,
		grpcclient.WithInsecure(),
		grpcclient.WithComponent("grpcinttest"),
		grpcclient.WithTimeout(10*time.Second),
		grpcclient.WithRetryPolicy(grpcclient.RetryPolicy{
			Methods:           []string{itemv1.ItemService_GetItem_FullMethodName, itemv1.ItemService_ListItems_FullMethodName},
			MaxAttempts:       3,
			InitialBackoff:    100 * time.Millisecond,
			MaxBackoff:        time.Second,
			BackoffMultiplier: 2,
			RetryableCodes:    []codes.Code{codes.Unavailable},
		}),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// newItemName returns an item name not used by any other test.
func newItemName(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(itemNameSeq.Add(1), 10)
}

// restDo sends a REST request and returns the status code and the body.
func restDo(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	require.NoError(t, err)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, data
}

// requireCode checks that err is a gRPC status error with the given code.
func requireCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	st, ok := status.FromError(err)
	require.True(t, ok, "not a gRPC status error: %v", err)
	require.Equal(t, want, st.Code(), st.Message())
}

func TestIntegrationHealth(t *testing.T) {
	t.Parallel()

	grpcAddr, _ := integrationEnv(t)
	client := healthpb.NewHealthClient(newIntegrationConn(t, grpcAddr))

	for _, svc := range []string{"", "db_main", "db_read"} {
		resp, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{Service: svc})
		require.NoError(t, err, svc)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus(), svc)
	}

	_, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "unknown"})
	requireCode(t, err, codes.NotFound)
}

func TestIntegrationTraceID(t *testing.T) {
	t.Parallel()

	grpcAddr, _ := integrationEnv(t)
	client := itemv1.NewItemServiceClient(newIntegrationConn(t, grpcAddr))

	const traceID = "grpc-integration-trace-id"

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-request-id", traceID)

	var header metadata.MD

	_, err := client.ListItems(ctx, &itemv1.ListItemsRequest{Limit: 1}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, []string{traceID}, header.Get("x-request-id"))
}

func TestIntegrationItemLifecycle(t *testing.T) {
	t.Parallel()

	grpcAddr, publicURL := integrationEnv(t)
	client := itemv1.NewItemServiceClient(newIntegrationConn(t, grpcAddr))
	name := newItemName("grpc-lifecycle")

	created, err := client.CreateItem(t.Context(), &itemv1.CreateItemRequest{Name: name, Quantity: 42})
	require.NoError(t, err)

	it := created.GetItem()
	require.NotEmpty(t, it.GetId())
	require.Equal(t, name, it.GetName())
	require.Equal(t, uint32(42), it.GetQuantity())
	require.False(t, it.GetCreatedAt().AsTime().IsZero())

	got, err := client.GetItem(t.Context(), &itemv1.GetItemRequest{Id: it.GetId()})
	require.NoError(t, err)
	require.Equal(t, it.GetId(), got.GetItem().GetId())
	require.Equal(t, name, got.GetItem().GetName())
	require.True(t, it.GetCreatedAt().AsTime().Equal(got.GetItem().GetCreatedAt().AsTime()))

	list, err := client.ListItems(t.Context(), &itemv1.ListItemsRequest{Limit: 100})
	require.NoError(t, err)
	require.NotEmpty(t, list.GetItems())
	require.LessOrEqual(t, len(list.GetItems()), 100)

	// The item created over gRPC is visible over REST.
	code, body := restDo(t, http.MethodGet, publicURL+"/items/"+it.GetId(), "")
	require.Equal(t, http.StatusOK, code)

	var rest restItem

	require.NoError(t, json.Unmarshal(body, &rest))
	require.Equal(t, restItem{ID: it.GetId(), Name: name, Quantity: 42}, rest)

	_, err = client.DeleteItem(t.Context(), &itemv1.DeleteItemRequest{Id: it.GetId()})
	require.NoError(t, err)

	_, err = client.GetItem(t.Context(), &itemv1.GetItemRequest{Id: it.GetId()})
	requireCode(t, err, codes.NotFound)

	code, _ = restDo(t, http.MethodGet, publicURL+"/items/"+it.GetId(), "")
	require.Equal(t, http.StatusNotFound, code)
}

func TestIntegrationRESTToGRPC(t *testing.T) {
	t.Parallel()

	grpcAddr, publicURL := integrationEnv(t)
	client := itemv1.NewItemServiceClient(newIntegrationConn(t, grpcAddr))
	name := newItemName("rest-to-grpc")

	code, body := restDo(t, http.MethodPost, publicURL+"/items", fmt.Sprintf(`{"name":%q,"quantity":7}`, name))
	require.Equal(t, http.StatusCreated, code)

	var rest restItem

	require.NoError(t, json.Unmarshal(body, &rest))

	// The item created over REST is visible over gRPC.
	got, err := client.GetItem(t.Context(), &itemv1.GetItemRequest{Id: rest.ID})
	require.NoError(t, err)
	require.Equal(t, name, got.GetItem().GetName())
	require.Equal(t, uint32(7), got.GetItem().GetQuantity())

	_, err = client.DeleteItem(t.Context(), &itemv1.DeleteItemRequest{Id: rest.ID})
	require.NoError(t, err)

	code, _ = restDo(t, http.MethodDelete, publicURL+"/items/"+rest.ID, "")
	require.Equal(t, http.StatusNotFound, code)
}

func TestIntegrationErrors(t *testing.T) {
	t.Parallel()

	grpcAddr, _ := integrationEnv(t)
	client := itemv1.NewItemServiceClient(newIntegrationConn(t, grpcAddr))

	dup, err := client.CreateItem(t.Context(), &itemv1.CreateItemRequest{Name: newItemName("grpc-duplicate"), Quantity: 1})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = client.DeleteItem(context.Background(), &itemv1.DeleteItemRequest{Id: dup.GetItem().GetId()})
	})

	tests := []struct {
		name string
		call func(ctx context.Context) error
		want codes.Code
	}{
		{
			name: "CreateItem with a duplicate name",
			call: func(ctx context.Context) error {
				_, err := client.CreateItem(ctx, &itemv1.CreateItemRequest{Name: dup.GetItem().GetName()})
				return err //nolint:wrapcheck
			},
			want: codes.AlreadyExists,
		},
		{
			name: "CreateItem with an empty name",
			call: func(ctx context.Context) error {
				_, err := client.CreateItem(ctx, &itemv1.CreateItemRequest{})
				return err //nolint:wrapcheck
			},
			want: codes.InvalidArgument,
		},
		{
			name: "CreateItem with a name too long",
			call: func(ctx context.Context) error {
				_, err := client.CreateItem(ctx, &itemv1.CreateItemRequest{Name: strings.Repeat("x", 129)})
				return err //nolint:wrapcheck
			},
			want: codes.InvalidArgument,
		},
		{
			name: "ListItems with a limit above the maximum",
			call: func(ctx context.Context) error {
				_, err := client.ListItems(ctx, &itemv1.ListItemsRequest{Limit: 101})
				return err //nolint:wrapcheck
			},
			want: codes.InvalidArgument,
		},
		{
			name: "GetItem with an empty id",
			call: func(ctx context.Context) error {
				_, err := client.GetItem(ctx, &itemv1.GetItemRequest{})
				return err //nolint:wrapcheck
			},
			want: codes.InvalidArgument,
		},
		{
			name: "GetItem with an unknown id",
			call: func(ctx context.Context) error {
				_, err := client.GetItem(ctx, &itemv1.GetItemRequest{Id: unknownItemID})
				return err //nolint:wrapcheck
			},
			want: codes.NotFound,
		},
		{
			name: "DeleteItem with an unknown id",
			call: func(ctx context.Context) error {
				_, err := client.DeleteItem(ctx, &itemv1.DeleteItemRequest{Id: unknownItemID})
				return err //nolint:wrapcheck
			},
			want: codes.NotFound,
		},
		{
			name: "request above the maximum message size",
			call: func(ctx context.Context) error {
				_, err := client.CreateItem(ctx, &itemv1.CreateItemRequest{Name: strings.Repeat("x", MaxRecvMsgSize)})
				return err //nolint:wrapcheck
			},
			want: codes.ResourceExhausted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requireCode(t, tt.call(t.Context()), tt.want)
		})
	}
}
