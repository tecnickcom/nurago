package cli

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/nuragoexampleowner/nuragoexample/internal/metrics"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/bootstrap"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/httputil/jsendx"
	libmtr "github.com/tecnickcom/nurago/pkg/metrics"
)

type mockMetricsClientError struct{}

func (c *mockMetricsClientError) CreateMetricsClientFunc() (libmtr.Client, error) {
	return &libmtr.Default{}, nil
}

func (c *mockMetricsClientError) InstrumentDB(_ string, _ *sql.DB) error {
	return errors.New("TEST ERROR")
}

func (c *mockMetricsClientError) SqlOpen(driverName, dsn string) (*sql.DB, error) {
	return (&libmtr.Default{}).SqlOpen(driverName, dsn) //nolint:wrapcheck
}

func (c *mockMetricsClientError) IncExampleCounter(_ string) {}

// newValidDBMock registers a sqlmock database for the given DSN, optionally
// expecting the startup ping and health-check query.
func newValidDBMock(t *testing.T, dsn string, expect bool) (sqlmock.Sqlmock, func()) {
	t.Helper()

	dbMockDB, dbMock, err := sqlmock.NewWithDSN(dsn, sqlmock.MonitorPingsOption(true))
	require.NoError(t, err, "Unexpected error while creating sqlmock", err)

	if expect {
		dbMock.MatchExpectationsInOrder(false)
		dbMock.ExpectPing().WillReturnError(nil)

		rows := sqlmock.NewRows([]string{"1"}).AddRow("1")
		dbMock.ExpectQuery("SELECT 1").WillReturnRows(rows)
	}

	return dbMock, func() {
		_ = dbMockDB.Close()
	}
}

// Test_bindServiceHandlers pins the requirement that the item endpoints are
// registered only when a database is configured: with the database off the
// public server exposes /uid alone.
func Test_bindServiceHandlers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fcfg           func(cfg appConfig) appConfig
		wantPubRoutes  int
		wantPrivRoutes int
	}{
		{
			name: "binds no routes with the service disabled",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false

				return cfg
			},
			wantPubRoutes:  0,
			wantPrivRoutes: 0,
		},
		{
			name: "binds only /uid with the database disabled",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = true
				cfg.DB.Enabled = false

				return cfg
			},
			wantPubRoutes:  1,
			wantPrivRoutes: 1,
		},
		{
			name: "binds the item routes with the database enabled",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = true
				cfg.DB.Enabled = true
				cfg.DB.Main.DSN = "user:pass@tcp(host:3306)/database7"
				cfg.DB.Read.DSN = "user:pass@tcp(host:3306)/database8"

				return cfg
			},
			wantPubRoutes:  5,
			wantPrivRoutes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt.fcfg(getValidTestConfig())
			cfg.DB.Main.Driver = "sqlmock"
			cfg.DB.Read.Driver = "sqlmock"

			if cfg.DB.Enabled {
				_, cleanupMain := newValidDBMock(t, cfg.DB.Main.DSN, true)
				defer cleanupMain()

				_, cleanupRead := newValidDBMock(t, cfg.DB.Read.DSN, true)
				defer cleanupRead()
			}

			mtr := metrics.New()

			_, err := mtr.CreateMetricsClientFunc()
			require.NoError(t, err)

			appInfo := &jsendx.AppInfo{ProgramName: "test", ProgramVersion: "0.0.0", ProgramRelease: "0"}
			jsx := jsendx.NewJSXResp(httputil.NewHTTPResp(slog.Default()))

			priv, pub, status, err := bindServiceHandlers(
				t.Context(), &cfg, appInfo, jsx, slog.Default(), mtr, &sync.WaitGroup{}, make(chan struct{}),
			)

			require.NoError(t, err)
			require.NotNil(t, status)
			require.Len(t, priv.BindHTTP(t.Context()), tt.wantPrivRoutes)
			require.Len(t, pub.BindHTTP(t.Context()), tt.wantPubRoutes)
		})
	}
}

//nolint:gocognit,paralleltest,maintidx
func Test_bind(t *testing.T) {
	validDBMock := func(dsn string, expect bool) (sqlmock.Sqlmock, func()) {
		return newValidDBMock(t, dsn, expect)
	}

	tests := []struct {
		name           string
		fcfg           func(cfg appConfig) appConfig
		setupDBMain    func(dsn string, expect bool) (sqlmock.Sqlmock, func())
		setupDBRead    func(dsn string, expect bool) (sqlmock.Sqlmock, func())
		expectDBMain   bool
		expectDBRead   bool
		mtr            metrics.Metrics
		preBindAddr    string
		pingAddr       string
		wantErr        bool
		wantTimeoutErr bool
	}{
		{
			name: "succeed with disabled config and separate ports",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false
				cfg.Servers.Monitoring.Address = ":30041"
				cfg.Servers.Private.Address = ":30042"
				cfg.Servers.Public.Address = ":30043"

				return cfg
			},
			wantErr: false,
		},
		{
			name: "succeed with service enabled and database disabled",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = true
				cfg.DB.Enabled = false
				cfg.Servers.Monitoring.Address = ":30051"
				cfg.Servers.Private.Address = ":30052"
				cfg.Servers.Public.Address = ":30053"

				return cfg
			},
			wantErr: false,
		},
		{
			name: "fails with monitoring service port already bound",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false
				cfg.Servers.Monitoring.Address = ":30011"
				cfg.Servers.Private.Address = ":30012"
				cfg.Servers.Public.Address = ":30013"

				return cfg
			},
			preBindAddr:    ":30011",
			wantErr:        true,
			wantTimeoutErr: false,
		},
		{
			name: "fails with private service port already bound",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false
				cfg.Servers.Monitoring.Address = ":30021"
				cfg.Servers.Private.Address = ":30022"
				cfg.Servers.Public.Address = ":30023"

				return cfg
			},
			preBindAddr:    ":30022",
			wantErr:        true,
			wantTimeoutErr: false,
		},
		{
			name: "fails with public service port already bound",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false
				cfg.Servers.Monitoring.Address = ":30031"
				cfg.Servers.Private.Address = ":30032"
				cfg.Servers.Public.Address = ":30033"

				return cfg
			},
			preBindAddr:    ":30033",
			wantErr:        true,
			wantTimeoutErr: false,
		},
		{
			name: "fails ipify client configuration",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Enabled = false
				cfg.Clients.Ipify.Address = "%Z0_ipify"
				cfg.DB.Enabled = false

				return cfg
			},
			wantErr: true,
		},
		{
			name: "fails to instrument DB",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Enabled = true
				cfg.DB.Main.DSN = "user:pass@tcp(host:3306)/database1"
				cfg.DB.Read.DSN = "user:pass@tcp(host:3306)/database2"

				return cfg
			},
			setupDBMain:  validDBMock,
			setupDBRead:  validDBMock,
			expectDBMain: true,
			expectDBRead: false,
			mtr:          &mockMetricsClientError{},
			wantErr:      true,
		},
		{
			name: "fails main DB",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Enabled = true
				cfg.DB.Main.DSN = "user:pwd@tcp(db.invalid)/test-main"
				cfg.DB.Read.DSN = "user:pass@tcp(host:3306)/database3"

				return cfg
			},
			setupDBMain:  nil,
			setupDBRead:  validDBMock,
			expectDBMain: false,
			expectDBRead: false,
			wantErr:      true,
		},
		{
			name: "fails read DB",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Enabled = true
				cfg.DB.Main.DSN = "user:pass@tcp(host:3306)/database4"
				cfg.DB.Read.DSN = "user:pwd@tcp(db.invalid)/test-read"

				return cfg
			},
			setupDBMain:  validDBMock,
			expectDBMain: true,
			expectDBRead: false,
			wantErr:      true,
		},
		{
			name: "success with all features enabled",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Enabled = true
				cfg.DB.Main.DSN = "user:pass@tcp(host:3306)/database5"
				cfg.DB.Read.DSN = "user:pass@tcp(host:3306)/database6"

				return cfg
			},
			setupDBMain:  validDBMock,
			setupDBRead:  validDBMock,
			expectDBMain: true,
			expectDBRead: true,
			wantErr:      false,
		},
	}

	//nolint:paralleltest
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.preBindAddr != "" {
				var lc net.ListenConfig

				l, err := lc.Listen(t.Context(), "tcp", tt.preBindAddr)
				require.NoError(t, err)

				defer func() { _ = l.Close() }()
			}

			cfg := tt.fcfg(getValidTestConfig())
			wg := &sync.WaitGroup{}
			sc := make(chan struct{})

			var dbMockMain sqlmock.Sqlmock

			if tt.setupDBMain != nil {
				dbm, cleanupMain := tt.setupDBMain(
					cfg.DB.Main.DSN,
					tt.expectDBMain,
				)

				dbMockMain = dbm

				defer cleanupMain()
			}

			var dbMockRead sqlmock.Sqlmock

			if tt.setupDBRead != nil {
				dbr, cleanupRead := tt.setupDBRead(
					cfg.DB.Read.DSN,
					tt.expectDBRead,
				)

				dbMockRead = dbr

				defer cleanupRead()
			}

			var mtr metrics.Metrics

			if tt.mtr != nil {
				mtr = tt.mtr
			} else {
				mtr = metrics.New()
			}

			cfg.DB.Main.Driver = "sqlmock"
			cfg.DB.Read.Driver = "sqlmock"

			testBindFn := bind(
				&cfg,
				&jsendx.AppInfo{
					ProgramName:    "test",
					ProgramVersion: "0.0.0",
					ProgramRelease: "0",
				},
				mtr,
				wg,
				sc,
			)

			testCtx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
			defer cancel()

			testBootstrapOpts := []bootstrap.Option{
				bootstrap.WithContext(testCtx),
				bootstrap.WithLogger(slog.Default()),
				bootstrap.WithCreateMetricsClientFunc(mtr.CreateMetricsClientFunc),
				bootstrap.WithShutdownTimeout(5 * time.Second),
				bootstrap.WithShutdownWaitGroup(wg),
				bootstrap.WithShutdownSignalChan(sc),
			}

			err := bootstrap.Bootstrap(testBindFn, testBootstrapOpts...)

			if tt.wantErr {
				require.Error(t, err, "bind() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err, "bind() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantTimeoutErr {
				require.ErrorIs(t, err, context.DeadlineExceeded, "bind() error = %v, wantErr %v", err, context.DeadlineExceeded)
			} else {
				require.NotErrorIs(t, err, context.DeadlineExceeded, "bind() unexpected timeout error")
			}

			if dbMockMain != nil {
				require.NoError(t, dbMockMain.ExpectationsWereMet())
			}

			if dbMockRead != nil {
				require.NoError(t, dbMockRead.ExpectationsWereMet())
			}
		})
	}
}
