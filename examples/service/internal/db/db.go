// Package db handles the database.
package db

import (
	"context"
	"database/sql"
)

// SQLConn is the interface representing sqlconn.SQLConn.
type SQLConn interface {
	DB() *sql.DB
	HealthCheck(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// Databases holds the database connections.
//
// Enabled reports whether the connections were established. It gates the
// features that need a database: the item endpoints are registered only when it
// is true (see internal/cli/bind.go).
type Databases struct {
	Enabled bool
	Main    SQLConn
	Read    SQLConn
}
