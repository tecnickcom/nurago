/*
Package mysqllock provides process-distributed mutual exclusion using MySQL's
named lock primitives GET_LOCK and RELEASE_LOCK.

[MySQLLock.Acquire] requests a lock by key and returns a [ReleaseFunc] that must
be called to release it.

Usage:

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	locker := mysqllock.New(db)
	release, err := locker.Acquire(ctx, "daily-reconciliation", 10*time.Second)
	if err != nil {
		if errors.Is(err, mysqllock.ErrTimeout) {
			// Another instance is holding the lock.
			return
		}

		log.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			log.Printf("failed to release lock: %v", err)
		}
	}()

	// Perform the critical section while lock is held.

# Detecting lock loss

A named lock lives only for as long as its owning MySQL session. If that session
is dropped (idle-timeout reaping by MySQL or an intermediary proxy, network
failure, server restart), MySQL releases the lock even though the caller still
holds a [ReleaseFunc]. To keep the session active a periodic keep-alive query is
run while the lock is held; if that query fails the lock is presumed lost. Each
keep-alive attempt is bounded by a per-attempt timeout ([WithKeepAlivePingTimeout],
default 10s), so a connection that hangs (rather than resets) is also detected as
lock loss instead of stalling silently.

Pass [WithLostLockHandler] to [MySQLLock.Acquire] to be notified (with an error
wrapping [ErrLockLost]) the moment a specific lock is lost, so the critical
section can be aborted:

	release, err := locker.Acquire(ctx, key, timeout,
		mysqllock.WithLostLockHandler(func(err error) {
			cancelCriticalSection() // stop work; the lock is no longer held
		}))

# Features

  - Single-call acquisition API: [MySQLLock.Acquire] returns a release closure,
    so lock lifetime can be scoped with defer. The returned closure is
    idempotent and safe to call more than once, including concurrently.
  - Explicit timeout handling: [ErrTimeout] is returned when GET_LOCK does not
    acquire the lock within the requested timeout.
  - Input validation: empty or over-long keys yield [ErrInvalidKey] and
    non-positive timeouts yield [ErrInvalidTimeout], instead of surfacing an
    opaque server error.
  - Dedicated lock connection: each successful lock acquisition is tied to a
    dedicated SQL connection, matching MySQL's lock semantics.
  - Connection keep-alive: a periodic query keeps the lock-owning connection
    active for long-running critical sections; the interval is configurable with
    [WithKeepAliveInterval] and each attempt is bounded by
    [WithKeepAlivePingTimeout].
  - Lock-loss notification: a keep-alive failure is reported through the
    per-acquisition [WithLostLockHandler] and the instance-wide
    [WithKeepAliveErrorHandler], both receiving an error wrapping [ErrLockLost].
    Handler panics are recovered so a faulty handler cannot crash the process.
  - Bounded release: releasing the lock uses its own timeout (configurable with
    [WithReleaseTimeout]) so a wedged connection cannot block the caller forever.
  - Context-aware acquisition: caller context controls acquisition cancellation.
  - Zero external dependencies at runtime: relies only on database/sql and
    MySQL lock functions.

# Behavior Notes

The lock key namespace is per MySQL server instance. Use stable, descriptive
keys (for example, "service:job:daily-reconciliation"). Always call the
returned [ReleaseFunc], ideally with defer, to avoid holding locks longer than
intended. MySQL limits lock names to 64 characters; keys outside 1..64
characters are rejected with [ErrInvalidKey].

If the returned [ReleaseFunc] is never called, the keep-alive goroutine and its
connection stay alive (and the lock stays held) until the process exits; there is
no finalizer backstop, so releasing is the caller's responsibility.

The GET_LOCK timeout is passed to MySQL with sub-second (fractional) precision;
MySQL 5.7.5 and later accept fractional timeouts (older servers truncate to
whole seconds). The timeout must be positive; a non-positive value is rejected
with [ErrInvalidTimeout] (MySQL would otherwise treat a negative timeout as an
effectively unbounded wait). This lock timeout is distinct from the caller
context: if ctx is canceled or its deadline is shorter than timeout, acquisition
fails with a wrapped context error rather than [ErrTimeout], so callers that
distinguish "held by another instance" from "my own deadline" should also test
for context errors.

Because a released connection is returned to the pool rather than closed, the
explicit RELEASE_LOCK is required to free the named lock. In the rare case where
RELEASE_LOCK fails on an otherwise-healthy connection, that connection can return
to the pool still holding the lock; the same is true if the caller context is
canceled at the instant GET_LOCK grants the lock (the acquire path issues a
time-bounded best-effort RELEASE_LOCK to mitigate this without delaying the
canceled acquisition, but cannot guarantee it if the connection is already
unusable). Releasing also relies on the driver leaving the connection usable
after canceling an in-flight keep-alive query. Configuring db.SetConnMaxLifetime
bounds how long any such leaked lock can persist.

# When To Use

  - A scheduled job runs on every replica but must execute only once.
  - You already depend on MySQL and do not want to add a coordination
    service.
  - The lock must be released automatically if the holder's connection dies.
*/
package mysqllock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"
)

// ReleaseFunc releases an acquired lock and its associated SQL connection.
//
// It is returned by [MySQLLock.Acquire]. Callers should invoke it, typically
// using defer immediately after successful acquisition. It is idempotent and
// safe for concurrent use: the first call performs the release and subsequent
// calls return that same result.
type ReleaseFunc func() error

var (
	// ErrTimeout indicates that GET_LOCK did not acquire the lock before timeout.
	ErrTimeout = errors.New("mysqllock: acquire lock timeout")

	// ErrFailed indicates a non-timeout lock acquisition failure.
	ErrFailed = errors.New("mysqllock: failed to acquire a lock")

	// ErrNilDB indicates that Acquire was called on a lock manager built with a
	// nil *sql.DB.
	ErrNilDB = errors.New("mysqllock: nil database handle")

	// ErrInvalidKey indicates that the lock key is empty or exceeds MySQL's
	// 64-character named-lock limit.
	ErrInvalidKey = errors.New("mysqllock: invalid lock key")

	// ErrInvalidTimeout indicates that the acquisition timeout is not positive.
	ErrInvalidTimeout = errors.New("mysqllock: non-positive timeout")

	// ErrLockLost indicates that the lock was lost before it was explicitly
	// released, for example because the keep-alive connection failed or
	// RELEASE_LOCK reported the lock was no longer held by this session.
	ErrLockLost = errors.New("mysqllock: lock lost")
)

// MySQL lock result constants and SQL queries.
const (
	resLockError    = -1
	resLockTimeout  = 0
	resLockAcquired = 1

	sqlGetLock     = "SELECT COALESCE(GET_LOCK(?, ?), ?)"
	sqlReleaseLock = "SELECT COALESCE(RELEASE_LOCK(?), ?)"

	keepAliveSQLQuery = "DO 1"

	// maxKeyLen is MySQL's named-lock name length limit (since MySQL 5.7.5).
	maxKeyLen = 64

	defaultKeepAliveInterval    = 30 * time.Second
	defaultKeepAlivePingTimeout = 10 * time.Second
	defaultReleaseTimeout       = 10 * time.Second

	// bestEffortReleaseTimeout caps the acquire-path cleanup release so a
	// canceled acquisition returns promptly instead of waiting a full release
	// timeout on an already-unresponsive connection.
	bestEffortReleaseTimeout = 2 * time.Second
)

// MySQLLock acquires and releases MySQL named locks through a sql.DB pool.
//
// Create instances with [New].
type MySQLLock struct {
	db *sql.DB

	// keepAliveErrHandler, when set, is invoked with any error produced by the
	// keep-alive goroutine while a lock is held.
	keepAliveErrHandler func(error)

	// keepAliveInterval is the period between keep-alive queries.
	keepAliveInterval time.Duration

	// keepAlivePingTimeout bounds how long a single keep-alive query may run.
	keepAlivePingTimeout time.Duration

	// releaseTimeout bounds how long the RELEASE_LOCK query may run.
	releaseTimeout time.Duration
}

// acquireConfig holds per-acquisition settings assembled from [AcquireOption]s.
type acquireConfig struct {
	onLost func(error)
}

// New constructs distributed lock manager using MySQL named locks on provided database connection.
func New(db *sql.DB, opts ...Option) *MySQLLock {
	l := &MySQLLock{
		db:                   db,
		keepAliveInterval:    defaultKeepAliveInterval,
		keepAlivePingTimeout: defaultKeepAlivePingTimeout,
		releaseTimeout:       defaultReleaseTimeout,
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// Acquire acquires named lock with timeout, returning an idempotent release
// function and starting keep-alive management for the lock-owning connection.
//
// It returns [ErrInvalidKey] or [ErrInvalidTimeout] for invalid input,
// [ErrTimeout] if the lock is not acquired within timeout, and [ErrFailed] for
// other acquisition failures. Per-acquisition behavior (such as lock-loss
// notification via [WithLostLockHandler]) is configured through opts.
//
//nolint:contextcheck // keep-alive intentionally runs on a background-rooted context that outlives ctx.
func (l *MySQLLock) Acquire(ctx context.Context, key string, timeout time.Duration, opts ...AcquireOption) (ReleaseFunc, error) {
	if l.db == nil {
		return nil, ErrNilDB
	}

	verr := validateKey(key)
	if verr != nil {
		return nil, verr
	}

	if timeout <= 0 {
		return nil, ErrInvalidTimeout
	}

	acfg := &acquireConfig{}
	for _, opt := range opts {
		opt(acfg)
	}

	conn, err := l.getLockedConn(ctx, key, timeout)
	if err != nil {
		return nil, err
	}

	// The release context is independent from the parent context so the lock and
	// its keep-alive outlive the acquisition call.
	releaseCtx, cancelReleaseCtx := context.WithCancel(context.Background())

	// done is closed by the keep-alive goroutine right before it returns, so the
	// release function can wait for it to stop touching conn before using conn.
	done := make(chan struct{})

	releaseFunc := l.makeReleaseFunc(conn, key, cancelReleaseCtx, done)

	go keepConnectionAlive(releaseCtx, conn, l.keepAliveInterval, l.keepAlivePingTimeout, l.makeLostNotifier(key, acfg.onLost), done)

	return releaseFunc, nil
}

// validateKey rejects keys that MySQL cannot use as a named lock.
func validateKey(key string) error {
	if key == "" || utf8.RuneCountInString(key) > maxKeyLen {
		return fmt.Errorf("%w: must be 1..%d characters", ErrInvalidKey, maxKeyLen)
	}

	return nil
}

// getLockedConn checks out a dedicated connection and runs GET_LOCK on it,
// returning the connection only on successful acquisition. On any failure the
// connection is closed and the failure (including any close error) is returned.
func (l *MySQLLock) getLockedConn(ctx context.Context, key string, timeout time.Duration) (*sql.Conn, error) {
	conn, cerr := l.db.Conn(ctx)
	if cerr != nil {
		return nil, fmt.Errorf("unable to get mysql connection: %w", cerr)
	}

	row := conn.QueryRowContext(ctx, sqlGetLock, key, timeout.Seconds(), resLockError)

	var res int

	serr := row.Scan(&res)
	if serr != nil {
		err := fmt.Errorf("unable to scan mysql lock: %w", serr)

		// If ctx was canceled, GET_LOCK may have granted the lock server-side
		// before the result reached us; release it best-effort so it does not
		// leak on the pooled connection.
		if isContextError(serr) {
			//nolint:contextcheck // ctx is already canceled here; the cleanup must use a fresh context.
			l.bestEffortRelease(conn, key)
		}

		closeConnection(conn, &err)

		return nil, err
	}

	if res != resLockAcquired {
		err := ErrFailed
		if res == resLockTimeout {
			err = ErrTimeout
		}

		closeConnection(conn, &err)

		return nil, err
	}

	return conn, nil
}

// makeReleaseFunc builds an idempotent [ReleaseFunc]. The first call stops the
// keep-alive goroutine, waits for it to stop using conn, releases the lock and
// closes the connection; later calls return the first call's result unchanged.
func (l *MySQLLock) makeReleaseFunc(conn *sql.Conn, key string, cancel context.CancelFunc, done <-chan struct{}) ReleaseFunc {
	var (
		once   sync.Once
		result error
	)

	return func() error {
		once.Do(func() {
			// Stop the keep-alive goroutine and wait for it to exit BEFORE using the
			// connection, otherwise the keep-alive query and RELEASE_LOCK could run
			// on the same *sql.Conn concurrently (a database/sql concurrent-use
			// violation).
			cancel()
			<-done

			result = l.releaseLock(conn, key)
		})

		return result
	}
}

// releaseLock runs RELEASE_LOCK within the configured release timeout and closes
// the connection. RELEASE_LOCK is required because closing a *sql.Conn returns
// it to the pool rather than ending the MySQL session that owns the named lock.
// A result other than "released by this session" means the lock had already been
// lost and is reported as [ErrLockLost].
//
// The return value is named so the deferred closeConnection can join any
// connection-close error into it.
//
//nolint:nonamedreturns // deferred closeConnection must join into the returned error.
func (l *MySQLLock) releaseLock(conn *sql.Conn, key string) (err error) {
	defer closeConnection(conn, &err)

	ctx, cancel := context.WithTimeout(context.Background(), l.releaseTimeout)
	defer cancel()

	var res int

	serr := conn.QueryRowContext(ctx, sqlReleaseLock, key, resLockError).Scan(&res)
	if serr != nil {
		err = fmt.Errorf("unable to release mysql lock: %w", serr)
		return err
	}

	if res != resLockAcquired {
		err = fmt.Errorf("%w: RELEASE_LOCK for key %q returned %d", ErrLockLost, key, res)
	}

	return err
}

// bestEffortRelease attempts to release a lock that may have been granted just
// before the caller context was canceled on the acquire path. The outcome is
// ignored: the lock may never have been held, or the connection may be unusable
// after cancellation.
//
// It is bounded by bestEffortReleaseTimeout (capped by the configured release
// timeout) so it cannot add a long delay to an already-canceled acquisition.
func (l *MySQLLock) bestEffortRelease(conn *sql.Conn, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), min(l.releaseTimeout, bestEffortReleaseTimeout))
	defer cancel()

	_ = conn.QueryRowContext(ctx, sqlReleaseLock, key, resLockError).Scan(new(int))
}

// makeLostNotifier returns the callback invoked by the keep-alive goroutine on
// failure. It wraps the raw error with [ErrLockLost] and the lock key, then fans
// it out to the instance-wide keep-alive handler and the per-acquisition
// lost-lock handler when each is set.
func (l *MySQLLock) makeLostNotifier(key string, onLost func(error)) func(error) {
	return func(kerr error) {
		if l.keepAliveErrHandler == nil && onLost == nil {
			return
		}

		err := fmt.Errorf("%w for key %q: %w", ErrLockLost, key, kerr)

		safeInvoke(l.keepAliveErrHandler, err)
		safeInvoke(onLost, err)
	}
}

// safeInvoke calls handler with err, recovering from any panic so a faulty
// handler cannot crash the keep-alive goroutine (and thus the process) or stop a
// sibling handler from running. A nil handler is ignored.
func safeInvoke(handler func(error), err error) {
	if handler == nil {
		return
	}

	defer func() { _ = recover() }()

	handler(err)
}

// keepConnectionAlive periodically executes a simple query to keep the connection
// alive until ctx is canceled. It closes done once it has stopped using conn, so
// the release function can safely take over the connection. The first keep-alive
// failure that is not caused by ctx cancellation is reported to notify (as a lost
// lock); cancellation-induced failures are a normal release and are silent.
func keepConnectionAlive(ctx context.Context, conn *sql.Conn, interval, pingTimeout time.Duration, notify func(error), done chan<- struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	kerr := waitForKeepAliveFailure(ctx, conn, ticker, pingTimeout)

	// A failure not caused by ctx cancellation (the release path) is a lost lock.
	// Decide the verdict before closing done so a racing release() cannot flip it.
	lost := kerr != nil && ctx.Err() == nil

	// Close done (signaling that conn is no longer in use) BEFORE notifying: a
	// lost-lock handler may call the release function, which uses conn and waits
	// on done. Closing first lets it proceed instead of self-deadlocking, and is
	// safe because no further keep-alive queries run once a ping has failed.
	close(done)

	if lost {
		notify(kerr)
	}
}

// waitForKeepAliveFailure pings the connection on every interval tick until a
// ping fails (returning its error) or ctx is canceled (returning nil).
func waitForKeepAliveFailure(ctx context.Context, conn *sql.Conn, ticker *time.Ticker, pingTimeout time.Duration) error {
	for {
		select {
		case <-ticker.C:
			kerr := pingConnection(ctx, conn, pingTimeout)
			if kerr != nil {
				return kerr
			}

		case <-ctx.Done():
			return nil
		}
	}
}

// pingConnection runs the keep-alive statement once, bounded by its own timeout
// so a hung connection surfaces as an error instead of blocking forever. It uses
// DO, which evaluates its expression without producing a result set, so there
// are no rows to close.
func pingConnection(ctx context.Context, conn *sql.Conn, timeout time.Duration) error {
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := conn.ExecContext(pingCtx, keepAliveSQLQuery)
	if err != nil {
		return fmt.Errorf("unable to keep mysql connection alive: %w", err)
	}

	return nil
}

// isContextError reports whether err was caused by context cancellation or a
// deadline being exceeded.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// closeConnection closes the given SQL connection, joining any close error
// into err. When the close succeeds, err is left untouched so sentinel errors
// (e.g. [ErrTimeout]) keep their identity.
func closeConnection(conn *sql.Conn, err *error) {
	cerr := conn.Close()
	if cerr != nil {
		*err = errors.Join(*err, cerr)
	}
}
