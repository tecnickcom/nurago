package mysqllock_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/tecnickcom/nurago/pkg/mysqllock"
)

func ExampleMySQLLock_Acquire() {
	// A real program passes the *sql.DB of a MySQL connection. The mock
	// stands in for the GET_LOCK and RELEASE_LOCK round trips.
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	// GET_LOCK returns 1 when the lock is granted.
	mock.ExpectQuery("SELECT COALESCE\\(GET_LOCK").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))
	mock.ExpectQuery("SELECT COALESCE\\(RELEASE_LOCK").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))

	lock := mysqllock.New(db)

	// Acquire blocks until the lock is granted or timeout elapses. The
	// returned release function is idempotent.
	release, err := lock.Acquire(context.TODO(), "nightly-report", 5*time.Second)
	if err != nil {
		fmt.Println("acquire:", err)

		return
	}

	// Only one process across the fleet reaches this point for a given key.
	fmt.Println("running exclusive work")

	fmt.Println("release:", release())

	// Output:
	// running exclusive work
	// release: <nil>
}

func ExampleMySQLLock_Acquire_timeout() {
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	// GET_LOCK returns 0 when the timeout elapses with the lock held
	// elsewhere.
	mock.ExpectQuery("SELECT COALESCE\\(GET_LOCK").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(0))

	lock := mysqllock.New(db)

	_, err = lock.Acquire(context.TODO(), "nightly-report", time.Second)

	// Losing the race is an expected outcome, distinguishable from a real
	// failure through ErrTimeout.
	fmt.Println(errors.Is(err, mysqllock.ErrTimeout))

	// Output:
	// true
}
