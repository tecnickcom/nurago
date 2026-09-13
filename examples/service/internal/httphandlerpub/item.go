package httphandlerpub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	"github.com/tecnickcom/nurago/pkg/httpserver"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

// maxItemBodySize caps the request payload accepted by the item endpoints. The
// body holds a name and a quantity, so anything larger is rejected before it is
// decoded.
const maxItemBodySize = 4 * 1024

// createItemRequest is the wire shape of a create request.
//
// Quantity is kept raw rather than decoded straight into a uint32 because JSON
// has a single number type: a client is entitled to write a whole number as
// 3.0 or 3e0, and the standard decoder rejects both for an integer field.
// itemQuantity does the checking the decoder cannot.
type createItemRequest struct {
	Name     string          `json:"name"`
	Quantity json.RawMessage `json:"quantity"`
}

// itemRoutes returns the routes served by the item service. They are registered
// only when the service is set (see BindHTTP).
func (h *HTTPHandlerPublic) itemRoutes() []httpserver.Route {
	return []httpserver.Route{
		{
			Method:      http.MethodPost,
			Path:        "/items",
			Description: "Creates an item",
			Handler:     h.handleCreateItem,
		},
		{
			Method:      http.MethodGet,
			Path:        "/items",
			Description: "Lists a page of items",
			Handler:     h.handleListItems,
		},
		{
			Method:      http.MethodGet,
			Path:        "/items/:id",
			Description: "Returns a single item",
			Handler:     h.handleGetItem,
		},
		{
			Method:      http.MethodDelete,
			Path:        "/items/:id",
			Description: "Deletes a single item",
			Handler:     h.handleDeleteItem,
		},
	}
}

// handleCreateItem creates an item and answers 201 with a Location header.
func (h *HTTPHandlerPublic) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createItemRequest

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxItemBodySize))
	dec.DisallowUnknownFields()

	err := dec.Decode(&req)
	if err != nil {
		h.httpres.SendStatus(ctx, w, http.StatusBadRequest)

		return
	}

	quantity, err := itemQuantity(req.Quantity)
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	it, err := h.service.Create(ctx, item.CreateParams{Name: req.Name, Quantity: quantity})
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	w.Header().Set("Location", "/items/"+it.ID)
	h.httpres.SendJSON(ctx, w, http.StatusCreated, it)
}

// handleListItems answers with one page of items.
func (h *HTTPHandlerPublic) handleListItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params, err := itemListParams(r.URL.Query())
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	items, err := h.service.List(ctx, params)
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	h.httpres.SendJSON(ctx, w, http.StatusOK, items)
}

// handleGetItem answers with a single item, or 404 when it does not exist.
func (h *HTTPHandlerPublic) handleGetItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	it, err := h.service.Get(ctx, httputil.PathParam(r, "id"))
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	h.httpres.SendJSON(ctx, w, http.StatusOK, it)
}

// handleDeleteItem removes a single item and answers 204.
func (h *HTTPHandlerPublic) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := h.service.Delete(ctx, httputil.PathParam(r, "id"))
	if err != nil {
		h.sendItemError(ctx, w, err)

		return
	}

	// A 204 response carries no body, so the status is written directly instead
	// of through SendStatus, which writes the reason phrase as the body.
	w.WriteHeader(http.StatusNoContent)
}

// itemQuantity converts the raw JSON quantity to an item quantity.
//
// An absent quantity is zero. Anything else must be a JSON number holding a
// whole value within the range of the INT UNSIGNED column: a string, a null or
// a fraction is a validation failure, which keeps the rejection a 422 instead
// of letting the database refuse the insert with a 500.
func itemQuantity(raw json.RawMessage) (uint32, error) {
	if len(raw) == 0 {
		return 0, nil
	}

	var decoded any

	err := json.Unmarshal(raw, &decoded)

	v, ok := decoded.(float64)
	if err != nil || !ok || v != math.Trunc(v) || v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: quantity must be a whole number between 0 and %d", item.ErrValidation, uint32(math.MaxUint32))
	}

	return uint32(v), nil
}

// itemListParams reads the page selector from the query string.
//
// An absent limit selects the service default. A present one must be a
// non-negative integer: an unparsable value is rejected rather than silently
// replaced by the default, so the answer matches the documented contract.
func itemListParams(q url.Values) (item.ListParams, error) {
	if !q.Has("limit") {
		return item.ListParams{}, nil
	}

	v, err := strconv.ParseUint(q.Get("limit"), 10, 32)
	if err != nil {
		return item.ListParams{}, fmt.Errorf("%w: limit must be a non-negative integer", item.ErrValidation)
	}

	return item.ListParams{Limit: uint(v)}, nil
}

// sendItemError maps the item sentinel errors to HTTP status codes.
//
// Anything unrecognized is logged and answered with a 500 whose body says
// nothing, so an internal failure is diagnosable without being exposed.
func (h *HTTPHandlerPublic) sendItemError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, item.ErrNotFound):
		h.httpres.SendStatus(ctx, w, http.StatusNotFound)
	case errors.Is(err, item.ErrConflict):
		h.httpres.SendStatus(ctx, w, http.StatusConflict)
	case errors.Is(err, item.ErrValidation):
		h.httpres.SendStatus(ctx, w, http.StatusUnprocessableEntity)
	default:
		h.logger.With(slog.Any("error", err)).ErrorContext(ctx, "item request failed")
		h.httpres.SendStatus(ctx, w, http.StatusInternalServerError)
	}
}
