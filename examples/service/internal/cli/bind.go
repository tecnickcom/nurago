package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nuragoexampleowner/nuragoexample/internal/db"
	"github.com/nuragoexampleowner/nuragoexample/internal/httphandlerpriv"
	"github.com/nuragoexampleowner/nuragoexample/internal/httphandlerpub"
	"github.com/nuragoexampleowner/nuragoexample/internal/item"
	instr "github.com/nuragoexampleowner/nuragoexample/internal/metrics"
	"github.com/tecnickcom/nurago/pkg/bootstrap"
	"github.com/tecnickcom/nurago/pkg/healthcheck"
	"github.com/tecnickcom/nurago/pkg/httpclient"
	"github.com/tecnickcom/nurago/pkg/httpserver"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/httputil/jsendx"
	"github.com/tecnickcom/nurago/pkg/ipify"
	"github.com/tecnickcom/nurago/pkg/metrics"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/sqlconn"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

// bind returns the bootstrap bind function that wires the full runtime graph.
// It centralizes how handlers, clients, middleware, metrics, health checks, and
// servers are connected:
//
//   - Builds shared outbound HTTP client options (logging, tracing,
//     instrumentation) once and reuses them.
//   - Enables or disables optional components (service routes and databases)
//     from configuration.
//   - Starts monitoring, private, and public servers with consistent timeout,
//     middleware, panic, and shutdown behavior.
//   - Upgrades status reporting from default status to dependency-aware health
//     checks when enabled.
//
// The wiring is split into named helpers (newIpifyClient, bindServiceHandlers,
// newDatabases, startServiceServer).
func bind(cfg *appConfig, appInfo *jsendx.AppInfo, mtr instr.Metrics, wg *sync.WaitGroup, sc chan struct{}) bootstrap.BindFunc {
	return func(ctx context.Context, l *slog.Logger, m metrics.Client) error {
		jsx := jsendx.NewJSXResp(httputil.NewHTTPResp(l))

		// Sanitizes the HTTP dumps, query strings, and error URLs before they are
		// logged. One instance is shared by every client and server below.
		logRedactor := newLogRedactor()

		// Common outbound HTTP client options shared by every external client
		// (structured logging, trace propagation, and metrics instrumentation).
		// This base is built once and reused: each client constructor appends its
		// own timeout on top of it (see newIpifyClient). A real service that talks
		// to several upstreams reuses this same slice for each of them.
		httpClientOpts := []httpclient.Option{
			httpclient.WithLogger(l),
			httpclient.WithRoundTripper(m.InstrumentRoundTripper),
			httpclient.WithTraceIDHeaderName(traceid.DefaultHeader),
			httpclient.WithComponent(appInfo.ProgramName),
			httpclient.WithRedactFn(logRedactor.BytesToString),
		}

		// ipify is used only as a diagnostic (the monitoring /ip route); it is
		// intentionally not part of the health checks.
		ipifyClient, err := newIpifyClient(cfg, httpClientOpts)
		if err != nil {
			return err
		}

		serviceBinderPrivate, serviceBinderPublic, statusHandler, err := bindServiceHandlers(ctx, cfg, appInfo, jsx, l, mtr, wg, sc)
		if err != nil {
			return err
		}

		middleware := func(args httpserver.MiddlewareArgs, next http.Handler) http.Handler {
			return m.InstrumentHandler(args.Path, next.ServeHTTP)
		}

		// MONITORING SERVER

		httpMonitoringOpts := []httpserver.Option{
			httpserver.WithLogger(l),
			httpserver.WithServerAddr(cfg.Servers.Monitoring.Address),
			httpserver.WithRequestTimeout(time.Duration(cfg.Servers.Monitoring.Timeout) * time.Second),
			httpserver.WithMetricsHandlerFunc(m.MetricsHandlerFunc()),
			httpserver.WithTraceIDHeaderName(traceid.DefaultHeader),
			httpserver.WithMiddlewareFn(middleware),
			httpserver.WithNotFoundHandlerFunc(jsx.DefaultNotFoundHandlerFunc(appInfo)),
			httpserver.WithMethodNotAllowedHandlerFunc(jsx.DefaultMethodNotAllowedHandlerFunc(appInfo)),
			httpserver.WithPanicHandlerFunc(jsx.DefaultPanicHandlerFunc(appInfo)),
			httpserver.WithEnableAllDefaultRoutes(),
			httpserver.WithIndexHandlerFunc(jsx.DefaultIndexHandler(appInfo)),
			httpserver.WithIPHandlerFunc(jsx.DefaultIPHandler(appInfo, ipifyClient.GetPublicIP)),
			httpserver.WithPingHandlerFunc(jsx.DefaultPingHandler(appInfo)),
			httpserver.WithStatusHandlerFunc(statusHandler),
			httpserver.WithRedactFn(logRedactor.BytesToString),
			httpserver.WithShutdownWaitGroup(wg),
			httpserver.WithShutdownSignalChan(sc),
		}

		httpMonitoringServer, err := httpserver.New(ctx, httpserver.NopBinder(), httpMonitoringOpts...)
		if err != nil {
			return fmt.Errorf("error creating monitoring HTTP server: %w", err)
		}

		httpMonitoringServer.StartServer()

		// PRIVATE AND PUBLIC SERVERS
		//
		// Both expose only the default ping route plus their binder's routes, so
		// they are created through the shared startServiceServer helper.

		err = startServiceServer(ctx, "private", serviceBinderPrivate, cfgServer(cfg.Servers.Private), l, middleware, logRedactor, wg, sc)
		if err != nil {
			return err
		}

		err = startServiceServer(ctx, "public", serviceBinderPublic, cfgServer(cfg.Servers.Public), l, middleware, logRedactor, wg, sc)
		if err != nil {
			return err
		}

		// example of custom metric
		mtr.IncExampleCounter("START")

		return nil
	}
}

// newIpifyClient builds the ipify client used by the monitoring server's /ip
// diagnostic route.
//
// It takes the shared base HTTP client options and appends the ipify-specific
// timeout, keeping every outbound client consistent (logging, tracing, metrics)
// while each keeps its own timeout. The base slice is left untouched: it has no
// spare capacity, so the append always copies rather than mutating the caller's
// slice, which keeps it safe to reuse for the next client.
func newIpifyClient(cfg *appConfig, baseHTTPClientOpts []httpclient.Option) (*ipify.Client, error) {
	ipifyTimeout := time.Duration(cfg.Clients.Ipify.Timeout) * time.Second

	ipifyHTTPClient := httpclient.New(append(
		baseHTTPClientOpts,
		httpclient.WithTimeout(ipifyTimeout),
	)...)

	ipifyClient, err := ipify.New(
		ipify.WithHTTPClient(ipifyHTTPClient),
		ipify.WithTimeout(ipifyTimeout),
		ipify.WithURL(cfg.Clients.Ipify.Address),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build ipify client: %w", err)
	}

	return ipifyClient, nil
}

// bindServiceHandlers wires the private and public service binders together with
// the status handler.
//
// When the service is disabled it returns no-op binders and the default status
// handler. When enabled it attaches the real handlers and upgrades the status
// handler to a dependency-aware health check covering the databases.
func bindServiceHandlers(
	ctx context.Context,
	cfg *appConfig,
	appInfo *jsendx.AppInfo,
	jsx *jsendx.JSXResp,
	l *slog.Logger,
	mtr instr.Metrics,
	wg *sync.WaitGroup,
	sc chan struct{},
) (httpserver.Binder, httpserver.Binder, http.HandlerFunc, error) {
	if !cfg.Enabled {
		return httpserver.NopBinder(), httpserver.NopBinder(), jsx.DefaultStatusHandler(appInfo), nil
	}

	reldb, healthchecks, err := newDatabases(ctx, cfg, l, mtr, wg, sc)
	if err != nil {
		return nil, nil, nil, err
	}

	// The item feature needs a database. Without one the public handler is built
	// with a nil service and the item routes stay unregistered; see BindHTTP.
	//
	// itemSvc must be declared as the interface type and left nil: assigning a
	// nil *item.Service to it would produce a non-nil interface value, and the
	// routes would register against a service that can only fail.
	var itemSvc httphandlerpub.ItemService

	if reldb.Enabled {
		itemSvc = item.NewService(item.NewRepository(reldb.Main.DB(), reldb.Read.DB()))
	} else {
		l.InfoContext(ctx, "item endpoints disabled: no database configured")
	}

	// The private server has no business logic in this example, so its service is
	// nil. In a real service, replace nil with your service implementation: this
	// is the injection point for the private handlers.
	serviceBinderPrivate := httphandlerpriv.New(nil, l)
	serviceBinderPublic := httphandlerpub.New(itemSvc, l)

	// override the default status handler with a dependency-aware health check
	healthCheckHandler := healthcheck.NewHandler(
		healthchecks,
		healthcheck.WithLogger(l),
		healthcheck.WithResultWriter(jsx.HealthCheckResultWriter(appInfo)),
	)

	return serviceBinderPrivate, serviceBinderPublic, healthCheckHandler.ServeHTTP, nil
}

// newDatabases connects, instruments, and health-checks the main and read
// databases when the database is enabled.
//
// It returns the connections for the handlers to use and the health checks to
// register with the status handler. When the database is disabled it returns a
// disabled Databases value, an empty (non-nil) slice, and no error.
func newDatabases(
	ctx context.Context,
	cfg *appConfig,
	l *slog.Logger,
	mtr instr.Metrics,
	wg *sync.WaitGroup,
	sc chan struct{},
) (db.Databases, []healthcheck.HealthCheck, error) {
	healthchecks := []healthcheck.HealthCheck{}
	reldb := db.Databases{Enabled: cfg.DB.Enabled}

	if !cfg.DB.Enabled {
		return reldb, healthchecks, nil
	}

	var err error

	reldb.Main, healthchecks, err = newDatabase(ctx, "main", cfg.DB.Main, healthchecks, mtr, wg, sc)
	if err != nil {
		return db.Databases{}, nil, err
	}

	reldb.Read, healthchecks, err = newDatabase(ctx, "read", cfg.DB.Read, healthchecks, mtr, wg, sc)
	if err != nil {
		return db.Databases{}, nil, err
	}

	l.InfoContext(ctx, "database connections established",
		slog.Bool("main", reldb.Main != nil),
		slog.Bool("read", reldb.Read != nil),
	)

	return reldb, healthchecks, nil
}

// newLogRedactor builds the redactor that sanitizes the HTTP request and
// response dumps, query strings, and error URLs written to the logs.
//
// Wiring one is optional: httpclient, httpserver, and httpreverseproxy already
// redact with the shared default redactor ([redact.Default]) when the
// WithRedactFn option is omitted, so this function exists to show how to depart
// from that default when the service needs it. A [redact.Redactor] is immutable
// after construction and safe for concurrent use, so a single instance is shared
// by every client and server.
//
// The options below are illustrative; the full set also includes
// [redact.WithMarker] (replace the `***` placeholder) and [redact.WithoutRules]
// (turn off whole rule classes, as shown by the commented-out block). Deleting
// this function and the WithRedactFn options that reference it restores the
// default behavior.
func newLogRedactor() *redact.Redactor {
	return redact.New(
		// House-style secret field names the built-in keyword set does not cover.
		// Each argument is a single word matched against the tokenized key, so
		// "floof" redacts `floof`, `userFloof`, and `floof_id`.
		redact.WithExtraTokens("floof"),
		// Keep these built-in keywords readable: this service logs monetary
		// amounts that are not sensitive on their own.
		redact.WithoutTokens("amount", "balance"),
		// Redact only digit runs that look like a card number AND pass the Luhn
		// checksum. The default (off) is the safer over-redacting choice: it also
		// hides unrelated identifiers that share a card prefix and length.
		redact.WithLuhnCheck(true),
		// Whole rule classes can be switched off individually: uncomment the block
		// below, keeping only the classes this service does not need. Every class
		// left enabled keeps redacting, so this stays a safe partial opt-out.
		//
		// To turn redaction off entirely, do not disable all nine classes here;
		// pass redact.InsecureNoRedaction to the WithRedactFn options instead, so
		// the bypass is visible at the call sites it affects.
		//
		// redact.WithoutRules(
		// 	redact.RuleHeaders,
		// 	redact.RuleJSON,
		// 	redact.RuleURLEncoded,
		// 	redact.RuleXML,
		// 	redact.RuleUserinfo,
		// 	redact.RuleJWT,
		// 	redact.RuleVendorTokens,
		// 	redact.RulePEM,
		// 	redact.RuleCards,
		// ),
	)
}

// startServiceServer builds, starts, and registers a service HTTP server that
// exposes the default ping route plus the routes provided by binder.
//
// The private and public servers share this path because they differ only by
// name, address, timeout, and binder.
func startServiceServer(
	ctx context.Context,
	name string,
	binder httpserver.Binder,
	srv cfgServer,
	l *slog.Logger,
	middleware httpserver.MiddlewareFn,
	logRedactor *redact.Redactor,
	wg *sync.WaitGroup,
	sc chan struct{},
) error {
	opts := []httpserver.Option{
		httpserver.WithLogger(l),
		httpserver.WithServerAddr(srv.Address),
		httpserver.WithRequestTimeout(time.Duration(srv.Timeout) * time.Second),
		httpserver.WithMiddlewareFn(middleware),
		httpserver.WithTraceIDHeaderName(traceid.DefaultHeader),
		httpserver.WithEnableDefaultRoutes(httpserver.PingRoute),
		httpserver.WithRedactFn(logRedactor.BytesToString),
		httpserver.WithShutdownWaitGroup(wg),
		httpserver.WithShutdownSignalChan(sc),
	}

	server, err := httpserver.New(ctx, binder, opts...)
	if err != nil {
		return fmt.Errorf("error creating %s HTTP server: %w", name, err)
	}

	server.StartServer()

	return nil
}

// newDatabase creates, instruments, and health-checks a named SQL connection.
//
// It sets connection pool options, startup ping behavior, metrics
// instrumentation, and health integration.
func newDatabase(
	ctx context.Context,
	name string,
	dbcfg cfgDB,
	healthchecks []healthcheck.HealthCheck,
	mtr instr.Metrics,
	wg *sync.WaitGroup,
	sc chan struct{},
) (*sqlconn.SQLConn, []healthcheck.HealthCheck, error) {
	// The DSN may embed the driver name as a "<driver>://" prefix (for example
	// "pgx://postgres://user:pass@host:5432/db"); sqlconn.Connect parses it and
	// falls back to the configured db.*.driver when the prefix is absent (the
	// plain MySQL DSN format has no "://").
	//
	// Driver-specific DSN parameters belong in the configuration, not here:
	// appending a MySQL query string in this shared function would break the
	// Postgres DSNs the same code path supports. The MySQL configs set
	// "?parseTime=true", which the driver needs to scan DATETIME columns into
	// time.Time (and "columnsWithAlias=true" is what SQLX needs for projections
	// that use joins).
	// Ref.: https://pkg.go.dev/github.com/go-sql-driver/mysql#readme-usage
	sqlConnOpts := []sqlconn.Option{
		sqlconn.WithDefaultDriver(dbcfg.Driver),
		sqlconn.WithPingTimeout(time.Duration(dbcfg.TimeoutPing) * time.Second),
		sqlconn.WithConnMaxOpen(dbcfg.ConnMaxOpen),
		sqlconn.WithConnMaxIdleCount(dbcfg.ConnMaxIdleCount),
		sqlconn.WithConnMaxIdleTime(time.Duration(dbcfg.ConnMaxIdleTime) * time.Second),
		sqlconn.WithConnMaxLifetime(time.Duration(dbcfg.ConnMaxLifetime) * time.Second),
		sqlconn.WithShutdownWaitGroup(wg),
		sqlconn.WithShutdownSignalChan(sc),
		sqlconn.WithSQLOpenFunc(mtr.SqlOpen),
	}

	sqlConn, err := sqlconn.Connect(ctx, dbcfg.DSN, sqlConnOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to %s DB: %w", name, err)
	}

	err = mtr.InstrumentDB("db_"+name, sqlConn.DB())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to instrument %s DB: %w", name, err)
	}

	return sqlConn, append(healthchecks, healthcheck.New("db_"+name, sqlConn)), nil
}
