// Package grpchandler serves the item.v1.ItemService gRPC API on top of the
// item business service. It is the gRPC counterpart of the item routes in
// internal/httphandlerpub, and maps the same sentinel errors to gRPC codes.
package grpchandler

import (
	"context"
	"errors"
	"log/slog"

	itemv1 "github.com/nuragoexampleowner/nuragoexample/internal/gen/item/v1"
	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MaxRecvMsgSize is the maximum size in bytes of a request message accepted by
// the server. An item request holds a name and a quantity.
const MaxRecvMsgSize = 4 * 1024

// errInternal is the error reported to the client for unrecognized failures.
var errInternal = errors.New("internal error")

// ItemService is the business-logic contract this handler consumes.
type ItemService interface {
	Get(ctx context.Context, id string) (*item.Item, error)
	List(ctx context.Context, p item.ListParams) ([]item.Item, error)
	Create(ctx context.Context, p item.CreateParams) (*item.Item, error)
	Delete(ctx context.Context, id string) error
}

// GRPCHandler implements itemv1.ItemServiceServer.
type GRPCHandler struct {
	itemv1.UnimplementedItemServiceServer

	service ItemService
	logger  *slog.Logger
}

// New creates the gRPC item handler. A nil service disables the item service;
// see BindGRPC. A nil logger selects slog.Default.
func New(s ItemService, l *slog.Logger) *GRPCHandler {
	if l == nil {
		l = slog.Default()
	}

	return &GRPCHandler{
		service: s,
		logger:  l,
	}
}

// BindGRPC registers the item service on s. Nothing is registered when the
// handler has no service, so the server exposes only the health service.
func (h *GRPCHandler) BindGRPC(_ context.Context, s grpc.ServiceRegistrar) error {
	if h.service == nil {
		return nil
	}

	itemv1.RegisterItemServiceServer(s, h)

	return nil
}

// CreateItem stores a new item.
func (h *GRPCHandler) CreateItem(ctx context.Context, req *itemv1.CreateItemRequest) (*itemv1.CreateItemResponse, error) {
	it, err := h.service.Create(ctx, item.CreateParams{Name: req.GetName(), Quantity: req.GetQuantity()})
	if err != nil {
		return nil, h.itemError(ctx, err)
	}

	return &itemv1.CreateItemResponse{Item: toProto(it)}, nil
}

// ListItems returns one page of items.
func (h *GRPCHandler) ListItems(ctx context.Context, req *itemv1.ListItemsRequest) (*itemv1.ListItemsResponse, error) {
	items, err := h.service.List(ctx, item.ListParams{Limit: uint(req.GetLimit())})
	if err != nil {
		return nil, h.itemError(ctx, err)
	}

	resp := &itemv1.ListItemsResponse{Items: make([]*itemv1.Item, 0, len(items))}

	for i := range items {
		resp.Items = append(resp.Items, toProto(&items[i]))
	}

	return resp, nil
}

// GetItem returns a single item.
func (h *GRPCHandler) GetItem(ctx context.Context, req *itemv1.GetItemRequest) (*itemv1.GetItemResponse, error) {
	it, err := h.service.Get(ctx, req.GetId())
	if err != nil {
		return nil, h.itemError(ctx, err)
	}

	return &itemv1.GetItemResponse{Item: toProto(it)}, nil
}

// DeleteItem removes a single item.
func (h *GRPCHandler) DeleteItem(ctx context.Context, req *itemv1.DeleteItemRequest) (*itemv1.DeleteItemResponse, error) {
	err := h.service.Delete(ctx, req.GetId())
	if err != nil {
		return nil, h.itemError(ctx, err)
	}

	return &itemv1.DeleteItemResponse{}, nil
}

// itemError maps the item sentinel errors to gRPC status errors.
//
// The status message is the sentinel text, without the wrapped details. Any
// other error is logged and answered with codes.Internal and a generic message.
func (h *GRPCHandler) itemError(ctx context.Context, err error) error {
	var code codes.Code

	switch {
	case errors.Is(err, item.ErrNotFound):
		code, err = codes.NotFound, item.ErrNotFound
	case errors.Is(err, item.ErrConflict):
		code, err = codes.AlreadyExists, item.ErrConflict
	case errors.Is(err, item.ErrValidation):
		code, err = codes.InvalidArgument, item.ErrValidation
	default:
		h.logger.With(slog.Any("error", err)).ErrorContext(ctx, "item request failed")
		code, err = codes.Internal, errInternal
	}

	return status.Error(code, err.Error()) //nolint:wrapcheck
}

// toProto converts an item to its wire representation.
func toProto(it *item.Item) *itemv1.Item {
	return &itemv1.Item{
		Id:        it.ID,
		Name:      it.Name,
		Quantity:  it.Quantity,
		CreatedAt: timestamppb.New(it.CreatedAt),
	}
}
