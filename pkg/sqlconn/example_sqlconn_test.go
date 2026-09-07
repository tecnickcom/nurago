package sqlconn_test

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/tecnickcom/nurago/pkg/sqlconn"
)

func ExampleNew() {
	// A real program registers a driver (for example go-sql-driver/mysql)
	// and lets sqlconn call sql.Open. WithSQLOpenFunc substitutes a mock so
	// the example needs no database.
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	// The connection check runs the validation query, and Shutdown closes
	// the pool.
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectClose()

	conn, err := sqlconn.New(
		context.TODO(),
		"mysql",
		"user:pass@tcp(127.0.0.1:3306)/testdb",
		sqlconn.WithConnMaxOpen(25),
		sqlconn.WithConnMaxIdleCount(5),
		sqlconn.WithConnMaxLifetime(5*time.Minute),
		sqlconn.WithPingTimeout(2*time.Second),
		sqlconn.WithSQLOpenFunc(func(_, _ string) (*sql.DB, error) {
			return mockDB, nil
		}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = conn.Shutdown(context.TODO()) }()

	// DB returns the pooled *sql.DB for normal query execution.
	fmt.Println(conn.DB() != nil)

	// Output:
	// true
}

func ExampleConnect() {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectClose()

	// Connect takes a single DSN URL and derives the driver from its scheme,
	// which suits configuration supplied as one environment variable.
	conn, err := sqlconn.Connect(
		context.TODO(),
		"mysql://user:pass@tcp(127.0.0.1:3306)/testdb",
		sqlconn.WithSQLOpenFunc(func(_, _ string) (*sql.DB, error) {
			return mockDB, nil
		}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = conn.Shutdown(context.TODO()) }()

	fmt.Println(conn.DB() != nil)

	// Output:
	// true
}

func ExampleSQLConn_HealthCheck() {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	// One validation query on connect, a second one for the health probe,
	// then the close performed by Shutdown.
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectClose()

	conn, err := sqlconn.New(
		context.TODO(),
		"mysql",
		"user:pass@tcp(127.0.0.1:3306)/testdb",
		sqlconn.WithSQLOpenFunc(func(_, _ string) (*sql.DB, error) {
			return mockDB, nil
		}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = conn.Shutdown(context.TODO()) }()

	// HealthCheck satisfies healthcheck.HealthChecker, so the database can
	// be registered directly on a service health endpoint.
	fmt.Println(conn.HealthCheck(context.TODO()))

	// Output:
	// <nil>
}
