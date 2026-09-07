package sqltransaction_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/tecnickcom/nurago/pkg/sqltransaction"
)

func ExampleExec() {
	// A real program would use a *sql.DB from sql.Open. The mock keeps the
	// example self-contained and its output deterministic.
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE balance").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// The transaction is committed when run returns nil, and rolled back
	// when it returns an error or panics.
	err = sqltransaction.Exec(
		context.TODO(),
		db,
		func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE balance SET amount = amount - 10 WHERE id = 1")
			if err != nil {
				return fmt.Errorf("debiting account: %w", err)
			}

			return nil
		},
	)

	fmt.Println("committed:", err)

	// Output:
	// committed: <nil>
}

func ExampleExec_rollback() {
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectRollback()

	// Returning an error from run rolls the transaction back. The error is
	// wrapped, so errors.Is and errors.As still match the original.
	err = sqltransaction.Exec(
		context.TODO(),
		db,
		func(_ context.Context, _ *sql.Tx) error {
			return errors.New("insufficient funds")
		},
	)

	fmt.Println("rolled back:", err)

	// Output:
	// rolled back: failed executing a function inside SQL transaction: insufficient funds
}

func ExampleExecWithOptions() {
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT SUM").WillReturnRows(
		sqlmock.NewRows([]string{"total"}).AddRow(42),
	)
	mock.ExpectCommit()

	opts := &sql.TxOptions{
		Isolation: sql.LevelSerializable,
		ReadOnly:  true,
	}

	var total int

	err = sqltransaction.ExecWithOptions(
		context.TODO(),
		db,
		func(ctx context.Context, tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT SUM(amount) FROM balance").Scan(&total)
		},
		opts,
	)

	fmt.Println(total, err)

	// Output:
	// 42 <nil>
}
