/*
Package sqltransaction executes business logic inside a transaction with
begin/commit/rollback control flow and consistent error handling.

# How It Works

[Exec] and [ExecWithOptions] execute a caller-provided [ExecFunc] inside a
transaction:

 1. Start transaction via [DB.BeginTx].
 2. Run the provided function with `*sql.Tx`.
 3. Commit on success.
 4. Roll back automatically if the function fails or commit is not reached.

Rollback behavior is guarded to avoid noisy false failures:

  - rollback is skipped after successful commit,
  - `sql.ErrTxDone` during rollback is ignored,
  - rollback failures are joined with the current error for full diagnostics.

# Usage

	err := sqltransaction.Exec(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
	    // Execute all related SQL operations using tx.
	    // Return an error to trigger rollback.
	    return nil
	})
	if err != nil {
	    return err
	}

For a similar helper using github.com/jmoiron/sqlx instead of database/sql,
see:
github.com/tecnickcom/nurago/pkg/sqlxtransaction

# When To Use

  - Several statements must succeed or fail together.
  - You want rollback guaranteed on error and on panic, without repeating the
    boilerplate.
*/
package sqltransaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	// ErrNilDB is returned when the provided DB is nil.
	ErrNilDB = errors.New("db must not be nil")

	// ErrNilExecFunc is returned when the provided ExecFunc is nil.
	ErrNilExecFunc = errors.New("exec function must not be nil")
)

// ExecFunc is the type of the function to be executed inside a SQL Transaction.
type ExecFunc func(ctx context.Context, tx *sql.Tx) error

// DB defines the transaction entry point required by [Exec] and [ExecWithOptions].
type DB interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Exec executes function inside SQL transaction with automatic rollback on error and guarded cleanup.
func Exec(ctx context.Context, db DB, run ExecFunc) error {
	return ExecWithOptions(ctx, db, run, nil)
}

// ExecWithOptions executes function in SQL transaction with custom isolation level or read-only option; returns joined errors if commit/rollback both fail.
func ExecWithOptions(ctx context.Context, db DB, run ExecFunc, opts *sql.TxOptions) (err error) {
	if db == nil {
		return ErrNilDB
	}

	if run == nil {
		return ErrNilExecFunc
	}

	// committed gates the deferred rollback. It is a flag rather than an
	// `err != nil` check so the transaction is still rolled back when run panics:
	// during a panic the named return is nil, so an error check would leak the tx.
	var committed bool

	tx, berr := db.BeginTx(ctx, opts)
	if berr != nil {
		return fmt.Errorf("unable to start SQL transaction: %w", berr)
	}

	defer func() {
		if committed {
			return
		}

		kerr := tx.Rollback()
		if kerr != nil && !errors.Is(kerr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("failed rolling back SQL transaction: %w", kerr))
		}
	}()

	rerr := run(ctx, tx)
	if rerr != nil {
		return fmt.Errorf("failed executing a function inside SQL transaction: %w", rerr)
	}

	cerr := tx.Commit()
	if cerr != nil {
		return fmt.Errorf("unable to commit SQL transaction: %w", cerr)
	}

	committed = true

	return nil
}
