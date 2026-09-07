package sqlxtransaction_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/tecnickcom/nurago/pkg/sqlxtransaction"
)

func ExampleExec() {
	// A real program would use a *sqlx.DB from sqlx.Connect. The mock keeps
	// the example self-contained and its output deterministic.
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	db := sqlx.NewDb(mockDB, "sqlmock")

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO audit").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// The transaction is committed when run returns nil, and rolled back
	// when it returns an error or panics.
	err = sqlxtransaction.Exec(
		context.TODO(),
		db,
		func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO audit (event) VALUES ('login')")
			if err != nil {
				return fmt.Errorf("writing audit row: %w", err)
			}

			return nil
		},
	)

	fmt.Println("committed:", err)

	// Output:
	// committed: <nil>
}

func ExampleExec_rollback() {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	db := sqlx.NewDb(mockDB, "sqlmock")

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectRollback()

	// Returning an error from run rolls the transaction back. The error is
	// wrapped, so errors.Is and errors.As still match the original.
	err = sqlxtransaction.Exec(
		context.TODO(),
		db,
		func(_ context.Context, _ *sqlx.Tx) error {
			return errors.New("validation failed")
		},
	)

	fmt.Println("rolled back:", err)

	// Output:
	// rolled back: failed executing a function inside SQLX transaction: validation failed
}

func ExampleExecWithOptions() {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	db := sqlx.NewDb(mockDB, "sqlmock")

	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name").WillReturnRows(
		sqlmock.NewRows([]string{"name"}).AddRow("alice"),
	)
	mock.ExpectCommit()

	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
		ReadOnly:  true,
	}

	var names []string

	err = sqlxtransaction.ExecWithOptions(
		context.TODO(),
		db,
		func(ctx context.Context, tx *sqlx.Tx) error {
			return tx.SelectContext(ctx, &names, "SELECT name FROM users")
		},
		opts,
	)

	fmt.Println(names, err)

	// Output:
	// [alice] <nil>
}
