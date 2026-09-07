/*
Package sqlxtransaction handles begin/commit/rollback control flow around
business logic executed inside a sqlx transaction.

# How It Works

[Exec] and [ExecWithOptions] accept a caller-provided [ExecFunc] and run it
inside a sqlx transaction:

 1. Start a transaction with [DB.BeginTxx].
 2. Execute the provided function with the transaction object.
 3. Commit if execution succeeds.
 4. Roll back automatically if execution fails or commit is not reached.

Rollback behavior is deferred and guarded:

  - rollback is skipped after a successful commit,
  - `sql.ErrTxDone` is ignored during deferred rollback,
  - rollback failures are joined with the current error so diagnostics are not
    lost.

# Usage

	err := sqlxtransaction.Exec(ctx, db, func(ctx context.Context, tx *sqlx.Tx) error {
	    // Execute all related SQL operations using tx.
	    // Return an error to trigger rollback.
	    return nil
	})
	if err != nil {
	    return err
	}

For a similar helper based on the standard database/sql package (instead of
github.com/jmoiron/sqlx), see:
github.com/tecnickcom/nurago/pkg/sqltransaction

# When To Use

  - You use sqlx and want struct scanning inside a transaction.
  - Rollback must be guaranteed on error and on panic.
*/
package sqlxtransaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

var (
	// ErrNilDB is returned when the provided DB is nil.
	ErrNilDB = errors.New("db must not be nil")

	// ErrNilExecFunc is returned when the provided ExecFunc is nil.
	ErrNilExecFunc = errors.New("exec function must not be nil")
)

// ExecFunc is the type of the function to be executed inside a SQL Transaction.
type ExecFunc func(ctx context.Context, tx *sqlx.Tx) error

// DB defines the transaction entry point required by [Exec] and [ExecWithOptions].
type DB interface {
	BeginTxx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error)
}

// Exec executes function inside sqlx transaction with automatic rollback on error and guarded cleanup.
func Exec(ctx context.Context, db DB, run ExecFunc) error {
	return ExecWithOptions(ctx, db, run, nil)
}

// ExecWithOptions executes function in sqlx transaction with custom isolation level or read-only option; returns joined errors if commit/rollback both fail.
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

	tx, berr := db.BeginTxx(ctx, opts)
	if berr != nil {
		return fmt.Errorf("unable to start SQLX transaction: %w", berr)
	}

	defer func() {
		if committed {
			return
		}

		kerr := tx.Rollback()
		if kerr != nil && !errors.Is(kerr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("failed rolling back SQLX transaction: %w", kerr))
		}
	}()

	rerr := run(ctx, tx)
	if rerr != nil {
		return fmt.Errorf("failed executing a function inside SQLX transaction: %w", rerr)
	}

	cerr := tx.Commit()
	if cerr != nil {
		return fmt.Errorf("unable to commit SQLX transaction: %w", cerr)
	}

	committed = true

	return nil
}
