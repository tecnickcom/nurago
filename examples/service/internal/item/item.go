// Package item implements the example business domain: a named thing with a
// quantity, stored in a relational database.
//
// The package is split in three layers that know only the one below:
// the service (rules), the Store interface it depends on, and the repository
// (SQL). The HTTP layer lives in internal/httphandlerpub.
package item

import (
	"errors"
	"time"
)

// Limits applied by the service to the values it accepts.
const (
	// MaxNameLength is the maximum item name length in characters, matching the
	// column width.
	MaxNameLength = 128

	// DefaultPageSize is the number of items returned by List when no limit is requested.
	DefaultPageSize = 20

	// MaxPageSize is the upper bound List applies to a requested limit.
	MaxPageSize = 100
)

// Sentinel errors returned by this package. They are wrapped with %w at each
// layer and matched with errors.Is by the HTTP handlers, which map them to
// status codes.
var (
	// ErrNotFound is returned when no item matches the requested identifier.
	ErrNotFound = errors.New("item not found")

	// ErrConflict is returned when an item with the same name already exists.
	ErrConflict = errors.New("item already exists")

	// ErrValidation is returned when the input does not satisfy the rules above.
	ErrValidation = errors.New("item validation failed")
)

// Item is a named thing with a quantity.
//
// Quantity is a uint32 because the column is an INT UNSIGNED: the Go type and
// the SQL type share the same range, so no value that reaches the repository
// can overflow the column.
type Item struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Quantity  uint32    `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateParams holds the fields accepted when creating an item.
type CreateParams struct {
	Name     string `json:"name"`
	Quantity uint32 `json:"quantity"`
}

// ListParams selects one page of items. A zero Limit means DefaultPageSize.
type ListParams struct {
	Limit uint
}
