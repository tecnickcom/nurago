/*
Package sqlconn manages a database/sql connection lifecycle in long-running Go
services: applying pool limits, verifying connectivity, exposing health checks,
and closing the connection on shutdown signals.

# How It Works

  - [New] creates a connection from explicit `driver` and `dsn` values.
  - [Connect] accepts a URL-like string in the form `<DRIVER>://<DSN>` and
    delegates to [New]. If only the DSN is provided, a driver can be supplied
    via [WithDefaultDriver].
  - The context passed to [New]/[Connect] bounds connection establishment only
    (dialing and the initial health check). It does NOT control the pool
    lifetime: a request- or timeout-scoped context will not close the pool when
    it ends. To close the pool on application shutdown, wire a long-lived context
    via [WithLifetimeContext] and/or a shutdown channel via
    [WithShutdownSignalChan], or call [SQLConn.Shutdown] directly.
  - On successful connect, pool settings are applied (`max idle/open`, idle
    time, lifetime), and a goroutine waits for a shutdown signal channel, a
    canceled lifetime context, or a direct [SQLConn.Shutdown] call.
  - When shutdown is triggered, [SQLConn.Shutdown] closes the underlying
    database handle, updates the shared shutdown wait group, and prevents
    further use by setting the internal DB pointer to nil. Shutdown is
    idempotent and also stops the watcher goroutine, so the watcher and a
    deferred call can both fire safely without leaking the goroutine.

# Usage

Minimal: the deferred [SQLConn.Shutdown] closes the pool and stops the watcher.
The context bounds establishment only, so it is safe to pass a short-lived one.

	c, err := sqlconn.Connect(
	    ctx,
	    "mysql://user:pass@tcp(localhost:3306)/appdb",
	    sqlconn.WithDefaultDriver("mysql"),
	)
	if err != nil {
	    return err
	}

	if err := c.HealthCheck(ctx); err != nil {
	    return err
	}

	defer c.Shutdown(ctx)

Service lifecycle: wire the connection into a shared shutdown channel and wait
group so a central signal closes every pool and the process waits for them.

	c, err := sqlconn.Connect(
	    ctx,
	    "mysql://user:pass@tcp(localhost:3306)/appdb",
	    sqlconn.WithDefaultDriver("mysql"),
	    sqlconn.WithShutdownSignalChan(shutdownCh), // close(shutdownCh) closes the pool
	    sqlconn.WithShutdownWaitGroup(&shutdownWG), // shutdownWG.Wait() blocks until closed
	)
	if err != nil {
	    return err
	}

# When To Use

  - Pool limits, a connectivity check, a health probe, and shutdown handling
    all belong in one place.
  - The connection must close cleanly when the service receives a shutdown
    signal.
*/
package sqlconn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Exported sentinel errors returned by connection setup and health checks so
// callers can match them with errors.Is.
var (
	// ErrNilContext is returned by [New]/[Connect] when the provided context is nil.
	ErrNilContext = errors.New("context is required")

	// ErrNilDB is returned when the configured connect (or sql open) function
	// yields a nil database handle without an error.
	ErrNilDB = errors.New("connect or sql open function returned a nil database handle")

	// ErrUnavailable is returned by [SQLConn.HealthCheck] when the connection has
	// already been shut down. Callers can match it with errors.Is to distinguish a
	// shut-down pool from a genuine connectivity failure.
	ErrUnavailable = errors.New("database is unavailable")
)

// ConnectFunc is the type of function called to perform the actual DB connection.
type ConnectFunc func(ctx context.Context, cfg *config) (*sql.DB, error)

// CheckConnectionFunc is the type of function called to perform a DB connection check.
type CheckConnectionFunc func(ctx context.Context, db *sql.DB) error

// SQLOpenFunc is the type of function called to open the DB. (Only for monkey patch testing).
type SQLOpenFunc func(driverName, dataSourceName string) (*sql.DB, error)

// SQLConn is the structure that helps to manage a SQL DB connection.
type SQLConn struct {
	cfg    *config
	db     *sql.DB
	done   chan struct{} // closed once by Shutdown to stop the watcher goroutine
	dbLock sync.RWMutex
}

// connect attempts to connect to a SQL database.
func (cfg *config) connect(ctx context.Context) (*SQLConn, error) {
	db, err := cfg.connectFunc(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Guard against a misbehaving connect function so a nil handle becomes a
	// clean error instead of a nil-pointer panic on the pool-setting calls below.
	if db == nil {
		return nil, ErrNilDB
	}

	db.SetConnMaxIdleTime(cfg.connMaxIdleTime)
	db.SetConnMaxLifetime(cfg.connMaxLifetime)
	db.SetMaxIdleConns(cfg.connMaxIdleCount)
	db.SetMaxOpenConns(cfg.connMaxOpenCount)

	c := SQLConn{
		cfg:  cfg,
		db:   db,
		done: make(chan struct{}),
	}

	// The watcher observes the lifetime context, never the establishment context
	// passed to New/Connect, so a short-lived connect context cannot tear the pool
	// down. It is intentionally independent from ctx (a configured lifetime context,
	// or ctx with cancellation detached), so cancellation of the connect context
	// never closes the pool.
	lifetimeCtx := cfg.lifetimeCtx //nolint:contextcheck // deliberately decoupled from the establishment context
	if lifetimeCtx == nil {
		lifetimeCtx = context.WithoutCancel(ctx)
	}

	// Register with the shutdown wait group before launching the watcher
	// goroutine so the matching Add(-1) in Shutdown can never run first.
	cfg.shutdownWaitGroup.Add(1)

	// Wait for a shutdown signal, a canceled lifetime context, or a direct
	// Shutdown call. The done case guarantees the watcher always terminates, so a
	// direct Shutdown (e.g. a deferred call) never leaks this goroutine even when
	// neither the signal channel nor the lifetime context ever fires.
	go func() {
		select {
		case <-cfg.shutdownSignalChan:
			cfg.logger.Debug("sqlconn shutdown signal received")
		case <-lifetimeCtx.Done():
			cfg.logger.Warn("sqlconn lifetime context canceled")
		case <-c.done:
			cfg.logger.Debug("sqlconn direct shutdown")
		}

		_ = c.Shutdown(lifetimeCtx)
	}()

	return &c, nil
}

// New constructs a SQL connection with pool tuning, health checks, and graceful
// shutdown orchestration. The provided context bounds connection establishment
// only; use [WithLifetimeContext] and/or [WithShutdownSignalChan] to control the
// pool lifetime.
func New(ctx context.Context, driver, dsn string, opts ...Option) (*SQLConn, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	cfg, err := newConfig(driver, dsn, opts...)
	if err != nil {
		return nil, err
	}

	return cfg.connect(ctx)
}

// Connect parses URL format "<DRIVER>://<DSN>" and delegates to New for connection creation.
func Connect(ctx context.Context, url string, opts ...Option) (*SQLConn, error) {
	driver, dsn := parseConnectionURL(url)

	return New(ctx, driver, dsn, opts...)
}

// DB returns current database connection from pool; may be nil after Shutdown().
func (c *SQLConn) DB() *sql.DB {
	c.dbLock.RLock()
	defer c.dbLock.RUnlock()

	return c.db
}

// HealthCheck verifies database connectivity with a ping and validation query,
// respecting the configured ping timeout. It returns [ErrUnavailable] when the
// connection has already been shut down.
//
// The read lock is intentionally held for the full ping+query round-trip so the
// underlying handle cannot be closed by a concurrent Shutdown mid-check; a
// Shutdown may therefore block for up to the ping timeout behind an in-flight
// health check.
func (c *SQLConn) HealthCheck(ctx context.Context) error {
	c.dbLock.RLock()
	defer c.dbLock.RUnlock()

	if c.db == nil {
		return ErrUnavailable
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.pingTimeout)
	defer cancel()

	return c.cfg.checkConnectionFunc(ctx, c.db)
}

// Shutdown gracefully closes database connection, preventing new queries and updating shutdown wait group.
// The context parameter is intentionally ignored: sql.DB.Close takes no context.
// Shutdown is idempotent; calling it more than once is a no-op and never drives the wait group negative.
func (c *SQLConn) Shutdown(_ context.Context) error {
	c.cfg.logger.Debug("shutting down sql connection")

	c.dbLock.Lock()
	defer c.dbLock.Unlock()

	if c.db == nil {
		c.cfg.logger.Debug("sql connection already shut down")

		return nil
	}

	err := c.db.Close()

	c.db = nil
	// Reached exactly once (guarded by the db == nil check under the write lock),
	// so done is closed a single time and unblocks the watcher goroutine.
	close(c.done)
	c.cfg.shutdownWaitGroup.Add(-1)

	c.cfg.logger.With(slog.Any("error", err)).Debug("sql connection shutdown complete")

	return err //nolint:wrapcheck
}

// checkConnection performs a ping and the configured validation query to verify
// the database connection is alive and able to execute statements. Ping is the
// portable liveness check; the query (default "SELECT 1", see [WithValidationQuery])
// additionally verifies the query path works.
func (c *config) checkConnection(ctx context.Context, db *sql.DB) error {
	perr := db.PingContext(ctx)
	if perr != nil {
		return fmt.Errorf("failed ping on database: %w", perr)
	}

	// Scan into any (not a typed int): the check only proves the query executed
	// and returned a row, so the validation query's single column may be of any
	// type (e.g. a string or driver-specific numeric).
	var probe any

	qerr := db.QueryRowContext(ctx, c.validationQuery).Scan(&probe)
	if qerr != nil {
		return fmt.Errorf("failed running check query on database: %w", qerr)
	}

	return nil
}

// connectOnce opens a database connection once and performs a single health check.
// It does not retry: any open or check failure is returned to the caller immediately.
func connectOnce(ctx context.Context, cfg *config) (*sql.DB, error) {
	db, err := cfg.sqlOpenFunc(cfg.driver, cfg.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed opening database connection: %w", err)
	}

	// Guard against a misbehaving sql open function so the check below does not
	// dereference a nil handle.
	if db == nil {
		return nil, ErrNilDB
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.pingTimeout)
	defer cancel()

	err = cfg.checkConnectionFunc(ctx, db)
	if err != nil {
		// Close the freshly opened pool (and any connection the check
		// established) so a failed check does not leak resources.
		return nil, errors.Join(fmt.Errorf("failed checking database connection: %w", err), db.Close())
	}

	return db, nil
}

// parseConnectionURL attempts to extract the driver/dsn pair from a string in the format <DRIVER>://<DSN>
// if only the DSN part is set, the driver will need to be specified via a configuration option.
// Examples:
// - mysql://user:pass@tcp(host:3306)/database
// - pgx://postgres://user:pass@host:5432/database?sslmode=disable
func parseConnectionURL(url string) (string, string) {
	if strings.TrimSpace(url) == "" {
		return "", ""
	}

	const sep = "://"

	before, after, found := strings.Cut(url, sep)

	if found {
		return before, after
	}

	return "", before
}
