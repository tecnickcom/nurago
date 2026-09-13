package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/tecnickcom/nurago/pkg/sqltransaction"
)

// This repository targets MySQL: the "?" placeholders and the duplicate-key
// mapping below are driver-specific (Postgres uses "$1" and its own SQLSTATE).
// A service supporting both engines needs a placeholder strategy and one error
// mapping per driver; this example supports one engine and says so.
const (
	// mysqlErrDupEntry is MySQL error 1062, raised by a violation of the
	// uk_item_name unique index.
	mysqlErrDupEntry = 1062

	sqlSelectItem = "SELECT id, name, quantity, created_at FROM item WHERE id = ?"
	sqlSelectPage = "SELECT id, name, quantity, created_at FROM item ORDER BY created_at DESC, id DESC LIMIT ?"
	sqlInsertItem = "INSERT INTO item (id, name, quantity, created_at) VALUES (?, ?, ?, ?)"
	sqlDeleteItem = "DELETE FROM item WHERE id = ?"
)

// Repository reads and writes items, and is the only place in this feature that
// names a column.
//
// Reads go to the read connection and writes to the main one, matching the two
// connections the service configures.
type Repository struct {
	write *sql.DB
	read  *sql.DB
}

// NewRepository creates an item repository over the write and read connections.
func NewRepository(write, read *sql.DB) *Repository {
	return &Repository{
		write: write,
		read:  read,
	}
}

// Get returns one item by identifier, or ErrNotFound when the row is absent.
func (r *Repository) Get(ctx context.Context, id string) (*Item, error) {
	var it Item

	row := r.read.QueryRowContext(ctx, sqlSelectItem, id)

	err := row.Scan(&it.ID, &it.Name, &it.Quantity, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	if err != nil {
		return nil, fmt.Errorf("failed scanning item row: %w", err)
	}

	return &it, nil
}

// List returns at most limit items, most recently created first.
func (r *Repository) List(ctx context.Context, limit uint) ([]Item, error) {
	rows, err := r.read.QueryContext(ctx, sqlSelectPage, limit)
	if err != nil {
		return nil, fmt.Errorf("failed querying items: %w", err)
	}

	defer func() { _ = rows.Close() }()

	items := []Item{}

	for rows.Next() {
		var it Item

		err = rows.Scan(&it.ID, &it.Name, &it.Quantity, &it.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed scanning item row: %w", err)
		}

		items = append(items, it)
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("failed reading item rows: %w", err)
	}

	return items, nil
}

// Create inserts one item, mapping a duplicate name to ErrConflict.
//
// A single insert does not need a transaction; it is written inside one because
// the transaction helper is the thing worth showing here, and one statement is
// the smallest example of it.
func (r *Repository) Create(ctx context.Context, it *Item) error {
	err := sqltransaction.Exec(ctx, r.write, func(ctx context.Context, tx *sql.Tx) error {
		_, ierr := tx.ExecContext(ctx, sqlInsertItem, it.ID, it.Name, it.Quantity, it.CreatedAt)

		return mapWriteError(ierr, it.Name)
	})
	if err != nil {
		return fmt.Errorf("failed inserting item: %w", err)
	}

	return nil
}

// Delete removes one item, or returns ErrNotFound when no row matched.
func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, sqlDeleteItem, id)
	if err != nil {
		return fmt.Errorf("failed deleting item: %w", err)
	}

	num, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed counting deleted items: %w", err)
	}

	if num == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return nil
}

// mapWriteError translates a MySQL duplicate-key failure into ErrConflict, so
// the unique index is reported as a conflict instead of an unknown failure.
func mapWriteError(err error, name string) error {
	if err == nil {
		return nil
	}

	var myerr *mysql.MySQLError

	if errors.As(err, &myerr) && myerr.Number == mysqlErrDupEntry {
		return fmt.Errorf("%w: %s", ErrConflict, name)
	}

	return err
}
