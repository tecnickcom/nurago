package grpchandler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	itemv1 "github.com/nuragoexampleowner/nuragoexample/internal/gen/item/v1"
	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// errTest stands for any failure the handler does not recognize.
var errTest = errors.New("TEST ERROR")

// testItem is the item returned by the fake service.
func testItem() item.Item {
	return item.Item{
		ID:        "019813a0-0000-7000-8000-000000000001",
		Name:      "test",
		Quantity:  3,
		CreatedAt: time.Date(2025, time.January, 1, 0, 0, 1, 0, time.UTC),
	}
}

// fakeItemService implements ItemService without a database and records the
// last parameters it received.
type fakeItemService struct {
	err        error
	lastID     string
	lastList   item.ListParams
	lastCreate item.CreateParams
}

func (f *fakeItemService) Get(_ context.Context, id string) (*item.Item, error) {
	f.lastID = id

	if f.err != nil {
		return nil, f.err
	}

	it := testItem()

	return &it, nil
}

func (f *fakeItemService) List(_ context.Context, p item.ListParams) ([]item.Item, error) {
	f.lastList = p

	if f.err != nil {
		return nil, f.err
	}

	return []item.Item{testItem(), testItem()}, nil
}

func (f *fakeItemService) Create(_ context.Context, p item.CreateParams) (*item.Item, error) {
	f.lastCreate = p

	if f.err != nil {
		return nil, f.err
	}

	it := testItem()

	return &it, nil
}

func (f *fakeItemService) Delete(_ context.Context, id string) error {
	f.lastID = id

	return f.err
}

// newTestClient serves h over an in-memory connection and returns a client.
func newTestClient(t *testing.T, h *GRPCHandler) itemv1.ItemServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()

	require.NoError(t, h.BindGRPC(t.Context(), srv))

	go func() { _ = srv.Serve(lis) }()

	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return itemv1.NewItemServiceClient(conn)
}

func TestNew(t *testing.T) {
	t.Parallel()

	h := New(nil, nil)
	require.NotNil(t, h)
	require.NotNil(t, h.logger)
	require.Nil(t, h.service)
}

func TestGRPCHandler_BindGRPC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service ItemService
		want    int
	}{
		{
			name:    "registers nothing without a service",
			service: nil,
			want:    0,
		},
		{
			name:    "registers the item service",
			service: &fakeItemService{},
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := grpc.NewServer()

			err := New(tt.service, slog.Default()).BindGRPC(t.Context(), srv)
			require.NoError(t, err)
			require.Len(t, srv.GetServiceInfo(), tt.want)
		})
	}
}

func TestGRPCHandler_methods(t *testing.T) {
	t.Parallel()

	want := testItem()
	svc := &fakeItemService{}
	client := newTestClient(t, New(svc, slog.Default()))

	created, err := client.CreateItem(t.Context(), &itemv1.CreateItemRequest{Name: "alpha", Quantity: 7})
	require.NoError(t, err)
	require.Equal(t, item.CreateParams{Name: "alpha", Quantity: 7}, svc.lastCreate)
	require.Equal(t, want.ID, created.GetItem().GetId())
	require.Equal(t, want.Name, created.GetItem().GetName())
	require.Equal(t, want.Quantity, created.GetItem().GetQuantity())
	require.True(t, want.CreatedAt.Equal(created.GetItem().GetCreatedAt().AsTime()))

	list, err := client.ListItems(t.Context(), &itemv1.ListItemsRequest{Limit: 5})
	require.NoError(t, err)
	require.Equal(t, item.ListParams{Limit: 5}, svc.lastList)
	require.Len(t, list.GetItems(), 2)

	got, err := client.GetItem(t.Context(), &itemv1.GetItemRequest{Id: want.ID})
	require.NoError(t, err)
	require.Equal(t, want.ID, svc.lastID)
	require.Equal(t, want.ID, got.GetItem().GetId())

	_, err = client.DeleteItem(t.Context(), &itemv1.DeleteItemRequest{Id: "x"})
	require.NoError(t, err)
	require.Equal(t, "x", svc.lastID)
}

func TestGRPCHandler_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name:     "not found",
			err:      fmt.Errorf("get item: %w", item.ErrNotFound),
			wantCode: codes.NotFound,
			wantMsg:  item.ErrNotFound.Error(),
		},
		{
			name:     "conflict",
			err:      fmt.Errorf("create item: %w", item.ErrConflict),
			wantCode: codes.AlreadyExists,
			wantMsg:  item.ErrConflict.Error(),
		},
		{
			name:     "validation",
			err:      fmt.Errorf("%w: empty name", item.ErrValidation),
			wantCode: codes.InvalidArgument,
			wantMsg:  item.ErrValidation.Error(),
		},
		{
			name:     "canceled",
			err:      fmt.Errorf("list items: %w", context.Canceled),
			wantCode: codes.Canceled,
			wantMsg:  context.Canceled.Error(),
		},
		{
			name:     "deadline exceeded",
			err:      fmt.Errorf("list items: %w", context.DeadlineExceeded),
			wantCode: codes.DeadlineExceeded,
			wantMsg:  context.DeadlineExceeded.Error(),
		},
		{
			name:     "internal",
			err:      errTest,
			wantCode: codes.Internal,
			wantMsg:  "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, New(&fakeItemService{err: tt.err}, slog.New(slog.DiscardHandler)))

			calls := map[string]func() error{
				"CreateItem": func() error {
					_, err := client.CreateItem(t.Context(), &itemv1.CreateItemRequest{Name: "a"})
					return err //nolint:wrapcheck
				},
				"ListItems": func() error {
					_, err := client.ListItems(t.Context(), &itemv1.ListItemsRequest{})
					return err //nolint:wrapcheck
				},
				"GetItem": func() error {
					_, err := client.GetItem(t.Context(), &itemv1.GetItemRequest{Id: "a"})
					return err //nolint:wrapcheck
				},
				"DeleteItem": func() error {
					_, err := client.DeleteItem(t.Context(), &itemv1.DeleteItemRequest{Id: "a"})
					return err //nolint:wrapcheck
				},
			}

			for method, call := range calls {
				st, ok := status.FromError(call())
				require.True(t, ok, method)
				require.Equal(t, tt.wantCode, st.Code(), method)
				require.Equal(t, tt.wantMsg, st.Message(), method)
			}
		})
	}
}
