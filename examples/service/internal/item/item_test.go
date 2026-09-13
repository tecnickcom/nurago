package item

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// errTest stands for any unrecognized failure coming from the store.
var errTest = errors.New("TEST ERROR")

// fakeStore is a hand-written Store used by the service tests. The service
// declares the Store interface, so its rules are testable without a database.
type fakeStore struct {
	item    *Item
	items   []Item
	created *Item
	limit   uint
	err     error
}

func (f *fakeStore) Get(_ context.Context, _ string) (*Item, error) {
	return f.item, f.err
}

func (f *fakeStore) List(_ context.Context, limit uint) ([]Item, error) {
	f.limit = limit

	return f.items, f.err
}

func (f *fakeStore) Create(_ context.Context, it *Item) error {
	f.created = it

	return f.err
}

func (f *fakeStore) Delete(_ context.Context, _ string) error {
	return f.err
}

func TestNewService(t *testing.T) {
	t.Parallel()

	require.NotNil(t, NewService(&fakeStore{}))
}

func TestService_Get(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		store   *fakeStore
		wantErr error
	}{
		{
			name:  "success",
			id:    "test-id",
			store: &fakeStore{item: &Item{ID: "test-id", Name: "test"}},
		},
		{
			name:    "fails with empty id",
			id:      "",
			store:   &fakeStore{},
			wantErr: ErrValidation,
		},
		{
			name:    "propagates the store error",
			id:      "test-id",
			store:   &fakeStore{err: ErrNotFound},
			wantErr: ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewService(tt.store).Get(t.Context(), tt.id)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.store.item, got)
		})
	}
}

func TestService_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		params    ListParams
		store     *fakeStore
		wantLimit uint
		wantErr   error
	}{
		{
			name:      "applies the default page size",
			params:    ListParams{},
			store:     &fakeStore{items: []Item{{ID: "test-id"}}},
			wantLimit: DefaultPageSize,
		},
		{
			name:      "honors a valid limit",
			params:    ListParams{Limit: 7},
			store:     &fakeStore{},
			wantLimit: 7,
		},
		{
			name:    "rejects an oversized limit",
			params:  ListParams{Limit: MaxPageSize + 1},
			store:   &fakeStore{},
			wantErr: ErrValidation,
		},
		{
			name:    "propagates the store error",
			params:  ListParams{},
			store:   &fakeStore{err: errTest},
			wantErr: errTest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewService(tt.store).List(t.Context(), tt.params)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantLimit, tt.store.limit)
			require.Equal(t, tt.store.items, got)
		})
	}
}

func TestService_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   CreateParams
		store    *fakeStore
		wantName string
		wantErr  error
	}{
		{
			name:     "success",
			params:   CreateParams{Name: "test", Quantity: 3},
			store:    &fakeStore{},
			wantName: "test",
		},
		{
			name:     "accepts a name of multi-byte characters at the length limit",
			params:   CreateParams{Name: strings.Repeat("è", MaxNameLength)},
			store:    &fakeStore{},
			wantName: strings.Repeat("è", MaxNameLength),
		},
		{
			name:    "fails with empty name",
			params:  CreateParams{Name: ""},
			store:   &fakeStore{},
			wantErr: ErrValidation,
		},
		{
			name:    "fails with an overlong name",
			params:  CreateParams{Name: strings.Repeat("a", MaxNameLength+1)},
			store:   &fakeStore{},
			wantErr: ErrValidation,
		},
		{
			name:    "propagates the store error",
			params:  CreateParams{Name: "test"},
			store:   &fakeStore{err: ErrConflict},
			wantErr: ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewService(tt.store).Create(t.Context(), tt.params)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, got.ID)
			require.Equal(t, tt.wantName, got.Name)
			require.Equal(t, tt.params.Quantity, got.Quantity)
			require.False(t, got.CreatedAt.IsZero())
			require.Equal(t, got.CreatedAt.Truncate(time.Microsecond), got.CreatedAt)
			require.Equal(t, got, tt.store.created)
		})
	}
}

func TestService_Delete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		store   *fakeStore
		wantErr error
	}{
		{
			name:  "success",
			id:    "test-id",
			store: &fakeStore{},
		},
		{
			name:    "fails with empty id",
			id:      "",
			store:   &fakeStore{},
			wantErr: ErrValidation,
		},
		{
			name:    "propagates the store error",
			id:      "test-id",
			store:   &fakeStore{err: ErrNotFound},
			wantErr: ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := NewService(tt.store).Delete(t.Context(), tt.id)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}
