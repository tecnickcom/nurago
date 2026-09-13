// Package httphandlerpub handles the inbound public service requests.
package httphandlerpub

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	"github.com/tecnickcom/nurago/pkg/httpserver"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/random"
)

// ItemService is the business-logic contract these handlers consume. It is
// declared here, by the consumer, so the handler package depends on a shape it
// controls rather than on a concrete implementation.
type ItemService interface {
	Get(ctx context.Context, id string) (*item.Item, error)
	List(ctx context.Context, p item.ListParams) ([]item.Item, error)
	Create(ctx context.Context, p item.CreateParams) (*item.Item, error)
	Delete(ctx context.Context, id string) error
}

// HTTPHandlerPublic is the struct containing all the http handlers.
type HTTPHandlerPublic struct {
	service ItemService
	httpres *httputil.HTTPResp
	logger  *slog.Logger
	rnd     *random.Rnd
}

// New creates a public API handler with shared response and UID utilities for
// endpoints intended for external consumers.
//
// A nil service disables the item endpoints; see BindHTTP.
func New(s ItemService, l *slog.Logger) *HTTPHandlerPublic {
	if l == nil {
		l = slog.Default()
	}

	return &HTTPHandlerPublic{
		service: s,
		httpres: httputil.NewHTTPResp(l),
		logger:  l,
		rnd:     random.New(nil),
	}
}

// BindHTTP returns the public routes exposed by this handler.
func (h *HTTPHandlerPublic) BindHTTP(_ context.Context) []httpserver.Route {
	uid := httpserver.Route{
		Method:      http.MethodGet,
		Path:        "/uid",
		Description: "Generates a random UID",
		Handler:     h.handleGenUID,
	}

	// The item endpoints need a database. When the service is nil the feature
	// is off, and the routes are left unregistered rather than bound to a
	// handler that can only fail.
	if h.service == nil {
		return []httpserver.Route{uid}
	}

	return append([]httpserver.Route{uid}, h.itemRoutes()...)
}

// handleGenUID responds with a UUIDv7 string in JSON format.
func (h *HTTPHandlerPublic) handleGenUID(w http.ResponseWriter, r *http.Request) {
	h.httpres.SendJSON(r.Context(), w, http.StatusOK, h.rnd.UUIDv7().String())
}
