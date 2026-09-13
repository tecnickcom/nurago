package httphandlerpub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	"github.com/stretchr/testify/require"
)

// errTest stands for any failure the handlers do not recognize.
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

// fakeItemService implements ItemService without a database.
type fakeItemService struct {
	err error
}

func (f *fakeItemService) Get(_ context.Context, _ string) (*item.Item, error) {
	if f.err != nil {
		return nil, f.err
	}

	it := testItem()

	return &it, nil
}

func (f *fakeItemService) List(_ context.Context, _ item.ListParams) ([]item.Item, error) {
	if f.err != nil {
		return nil, f.err
	}

	return []item.Item{testItem()}, nil
}

func (f *fakeItemService) Create(_ context.Context, _ item.CreateParams) (*item.Item, error) {
	if f.err != nil {
		return nil, f.err
	}

	it := testItem()

	return &it, nil
}

func (f *fakeItemService) Delete(_ context.Context, _ string) error {
	return f.err
}

// newItemRequest builds a request carrying the httprouter path parameters the
// item handlers read.
func newItemRequest(t *testing.T, method, target, body string, params httprouter.Params) *http.Request {
	t.Helper()

	ctx := context.WithValue(t.Context(), httprouter.ParamsKey, params)

	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	require.NoError(t, err)

	return req
}

// itemIDParams returns the path parameters matching the /items/:id routes.
func itemIDParams() httprouter.Params {
	return httprouter.Params{{Key: "id", Value: testItem().ID}}
}

func TestNew(t *testing.T) {
	t.Parallel()

	hh := New(nil, nil)
	require.NotNil(t, hh)
}

func TestHTTPHandlerPublic_BindHTTP(t *testing.T) {
	t.Parallel()

	t.Run("binds only /uid without a service", func(t *testing.T) {
		t.Parallel()

		got := New(nil, nil).BindHTTP(t.Context())
		require.Len(t, got, 1)
		require.Equal(t, "/uid", got[0].Path)
	})

	t.Run("binds the item routes with a service", func(t *testing.T) {
		t.Parallel()

		got := New(&fakeItemService{}, nil).BindHTTP(t.Context())
		require.Len(t, got, 5)
	})
}

func TestHTTPHandlerPublic_handleGenUID(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	hh := New(nil, nil)
	require.NotNil(t, hh)

	hh.handleGenUID(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	require.NotEmpty(t, string(body))
}

func TestHTTPHandlerPublic_handleCreateItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		svcErr   error
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"name":"test","quantity":3}`,
			wantCode: http.StatusCreated,
		},
		{
			name:     "fails with a malformed body",
			body:     `{"name":`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "accepts a whole quantity written with a fraction",
			body:     `{"name":"test","quantity":3.0}`,
			wantCode: http.StatusCreated,
		},
		{
			name:     "accepts an absent quantity",
			body:     `{"name":"test"}`,
			wantCode: http.StatusCreated,
		},
		{
			name:     "fails with an unknown field",
			body:     `{"name":"test","unknown":1}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "rejects a fractional quantity",
			body:     `{"name":"test","quantity":3.5}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects a negative quantity",
			body:     `{"name":"test","quantity":-1}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects a quantity above the column bound",
			body:     `{"name":"test","quantity":4294967296}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects a string quantity",
			body:     `{"name":"test","quantity":"3"}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects a null quantity",
			body:     `{"name":"test","quantity":null}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "maps a duplicate name to 409",
			body:     `{"name":"test"}`,
			svcErr:   item.ErrConflict,
			wantCode: http.StatusConflict,
		},
		{
			name:     "maps a validation failure to 422",
			body:     `{"name":""}`,
			svcErr:   item.ErrValidation,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "maps an unknown failure to 500",
			body:     `{"name":"test"}`,
			svcErr:   errTest,
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := newItemRequest(t, http.MethodPost, "/items", tt.body, nil)

			New(&fakeItemService{err: tt.svcErr}, nil).handleCreateItem(rr, req)

			require.Equal(t, tt.wantCode, rr.Code)

			if tt.wantCode != http.StatusCreated {
				return
			}

			require.Equal(t, "/items/"+testItem().ID, rr.Header().Get("Location"))

			var got item.Item

			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, testItem(), got)
		})
	}
}

func TestHTTPHandlerPublic_handleListItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		target   string
		svcErr   error
		wantCode int
	}{
		{
			name:     "success",
			target:   "/items",
			wantCode: http.StatusOK,
		},
		{
			name:     "success with a limit",
			target:   "/items?limit=1",
			wantCode: http.StatusOK,
		},
		{
			name:     "success with a zero limit",
			target:   "/items?limit=0",
			wantCode: http.StatusOK,
		},
		{
			name:     "rejects a limit that is not a number",
			target:   "/items?limit=none",
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects an empty limit",
			target:   "/items?limit=",
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "rejects an oversized limit",
			target:   "/items?limit=101",
			svcErr:   item.ErrValidation,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "maps an unknown failure to 500",
			target:   "/items",
			svcErr:   errTest,
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := newItemRequest(t, http.MethodGet, tt.target, "", nil)

			New(&fakeItemService{err: tt.svcErr}, nil).handleListItems(rr, req)

			require.Equal(t, tt.wantCode, rr.Code)

			if tt.wantCode != http.StatusOK {
				return
			}

			var got []item.Item

			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, []item.Item{testItem()}, got)
		})
	}
}

func TestHTTPHandlerPublic_handleGetItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		svcErr   error
		wantCode int
	}{
		{
			name:     "success",
			wantCode: http.StatusOK,
		},
		{
			name:     "maps a missing item to 404",
			svcErr:   item.ErrNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "maps an unknown failure to 500",
			svcErr:   errTest,
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := newItemRequest(t, http.MethodGet, "/items/"+testItem().ID, "", itemIDParams())

			New(&fakeItemService{err: tt.svcErr}, nil).handleGetItem(rr, req)

			require.Equal(t, tt.wantCode, rr.Code)

			if tt.wantCode != http.StatusOK {
				return
			}

			var got item.Item

			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, testItem(), got)
		})
	}
}

func TestHTTPHandlerPublic_handleDeleteItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		svcErr   error
		wantCode int
	}{
		{
			name:     "success",
			wantCode: http.StatusNoContent,
		},
		{
			name:     "maps a missing item to 404",
			svcErr:   item.ErrNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "maps an unknown failure to 500",
			svcErr:   errTest,
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := newItemRequest(t, http.MethodDelete, "/items/"+testItem().ID, "", itemIDParams())

			New(&fakeItemService{err: tt.svcErr}, nil).handleDeleteItem(rr, req)

			require.Equal(t, tt.wantCode, rr.Code)

			if tt.wantCode == http.StatusNoContent {
				require.Empty(t, rr.Body.String())
			}
		})
	}
}
