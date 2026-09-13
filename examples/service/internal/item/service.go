package item

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/tecnickcom/nurago/pkg/random"
)

// Store is the persistence contract the service depends on.
//
// It is declared here, by the consumer, so the service tests run against a
// hand-written fake and never touch a database. The repository in this same
// package is one implementation of it.
type Store interface {
	Get(ctx context.Context, id string) (*Item, error)
	List(ctx context.Context, limit uint) ([]Item, error)
	Create(ctx context.Context, it *Item) error
	Delete(ctx context.Context, id string) error
}

// Service holds the item business rules.
type Service struct {
	store Store
	rnd   *random.Rnd
}

// NewService creates an item service backed by the given store.
func NewService(s Store) *Service {
	return &Service{
		store: s,
		rnd:   random.New(nil),
	}
}

// Get returns the item with the given identifier, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (*Item, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty id", ErrValidation)
	}

	it, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get item: %w", err)
	}

	return it, nil
}

// List returns one page of items, most recently created first. A zero limit
// selects DefaultPageSize; a limit above MaxPageSize is rejected rather than
// silently reduced, so the caller is told the page it asked for is not served.
func (s *Service) List(ctx context.Context, p ListParams) ([]Item, error) {
	if p.Limit > MaxPageSize {
		return nil, fmt.Errorf("%w: limit above %d", ErrValidation, MaxPageSize)
	}

	limit := p.Limit

	if limit == 0 {
		limit = DefaultPageSize
	}

	items, err := s.store.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}

	return items, nil
}

// Create stores a new item, assigning it a UUIDv7 identifier and a creation
// time. The time is truncated to microseconds to match the DATETIME(6) column,
// so the returned item equals the stored one.
func (s *Service) Create(ctx context.Context, p CreateParams) (*Item, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("%w: empty name", ErrValidation)
	}

	// The bound is in characters, not bytes, because that is what the VARCHAR
	// column counts.
	if utf8.RuneCountInString(p.Name) > MaxNameLength {
		return nil, fmt.Errorf("%w: name longer than %d characters", ErrValidation, MaxNameLength)
	}

	it := &Item{
		ID:        s.rnd.UUIDv7().String(),
		Name:      p.Name,
		Quantity:  p.Quantity,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	err := s.store.Create(ctx, it)
	if err != nil {
		return nil, fmt.Errorf("create item: %w", err)
	}

	return it, nil
}

// Delete removes the item with the given identifier, or returns ErrNotFound.
func (s *Service) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrValidation)
	}

	err := s.store.Delete(ctx, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}

	return nil
}
