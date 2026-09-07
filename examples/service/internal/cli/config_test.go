package cli

import (
	"errors"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/config"
	"github.com/tecnickcom/nurago/pkg/validator"
)

func Test_appConfig_SetDefaults(t *testing.T) {
	t.Parallel()

	v := viper.New()
	c := &appConfig{}
	c.SetDefaults(v)

	require.True(t, v.GetBool("enabled"))
	require.Equal(t, dbDriver, v.GetString("db.main.driver"))
	require.Equal(t, dbDriver, v.GetString("db.read.driver"))
	require.Len(t, v.AllKeys(), 24)
}

func getValidTestConfig() appConfig {
	return appConfig{
		BaseConfig: config.BaseConfig{
			Log: config.LogConfig{
				Level:   "DEBUG",
				Format:  "CONSOLE",
				Network: "tcp",
				Address: "127.0.0.1:1234",
			},
			ShutdownTimeout: 2,
		},
		Clients: cfgClients{
			Ipify: cfgClientIpify{
				Address: "https://test.ipify.url.invalid",
				Timeout: 13,
			},
		},
		Enabled: true,
		Servers: cfgServers{
			Monitoring: cfgServerMonitoring{
				Address: ":1231",
				Timeout: 11,
			},
			Private: cfgServerPrivate{
				Address: ":1232",
				Timeout: 12,
			},
			Public: cfgServerPublic{
				Address: ":1233",
				Timeout: 13,
			},
		},
		DB: cfgDatabases{
			Enabled: true,
			Main: cfgDB{
				ConnMaxIdleCount: 2,
				ConnMaxIdleTime:  30,
				ConnMaxLifetime:  120,
				ConnMaxOpen:      10,
				Driver:           "mysql",
				DSN:              "user:pwd@tcp(db.invalid)/test-main",
				TimeoutPing:      2,
			},
			Read: cfgDB{
				ConnMaxIdleCount: 3,
				ConnMaxIdleTime:  31,
				ConnMaxLifetime:  121,
				ConnMaxOpen:      11,
				Driver:           "mysql",
				DSN:              "user:pwd@tcp(db.invalid)/test-read",
				TimeoutPing:      3,
			},
		},
	}
}

// Test_appConfig_Validate_validatorError covers the validator constructor
// error branch; it mutates the package-level validatorNewFn, so it must not
// run in parallel with the other Validate tests.
//
//nolint:paralleltest
func Test_appConfig_Validate_validatorError(t *testing.T) {
	oldValidatorNewFn := validatorNewFn

	defer func() { validatorNewFn = oldValidatorNewFn }()

	validatorNewFn = func(_ ...validator.Option) (*validator.Validator, error) {
		return nil, errors.New("TEST VALIDATOR ERROR")
	}

	cfg := getValidTestConfig()

	err := cfg.Validate()
	require.Error(t, err, "an error from the validator constructor was expected")
}

func Test_appConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fcfg    func(cfg appConfig) appConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			fcfg:    func(cfg appConfig) appConfig { return cfg },
			wantErr: false,
		},
		{
			name:    "empty log.level",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Level = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid log.level",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Level = "WRONG_LOG_LEVEL"; return cfg },
			wantErr: true,
		},
		{
			name:    "empty log.format",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Format = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid log.format",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Format = "WRONG_LOG_FORMAT"; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid log.network",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Network = "WRONG_LOG_NETWORK"; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid log.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Log.Address = "-WRONG_LOG_ADDRESS-"; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid shutdown_timeout",
			fcfg:    func(cfg appConfig) appConfig { cfg.ShutdownTimeout = -1; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers = cfgServers{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.monitoring",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Monitoring = cfgServerMonitoring{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.monitoring.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Monitoring.Address = ""; return cfg },
			wantErr: true,
		},
		{
			name: "invalid servers.monitoring.address",
			fcfg: func(cfg appConfig) appConfig {
				cfg.Servers.Monitoring.Address = "-WRONG_MONITORING_ADDRESS-"
				return cfg
			},
			wantErr: true,
		},
		{
			name:    "empty servers.monitoring.timeout",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Monitoring.Timeout = 0; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.private",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Private = cfgServerPrivate{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.private.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Private.Address = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid servers.private.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Private.Address = "-WRONG_PRIVATE_ADDRESS-"; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.private.timeout",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Private.Timeout = 0; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.public",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Public = cfgServerPublic{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.public.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Public.Address = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid servers.public.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Public.Address = "-WRONG_PUBLIC_ADDRESS-"; return cfg },
			wantErr: true,
		},
		{
			name:    "empty servers.public.timeout",
			fcfg:    func(cfg appConfig) appConfig { cfg.Servers.Public.Timeout = 0; return cfg },
			wantErr: true,
		},
		{
			name:    "empty clients",
			fcfg:    func(cfg appConfig) appConfig { cfg.Clients = cfgClients{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty clients.ipify",
			fcfg:    func(cfg appConfig) appConfig { cfg.Clients.Ipify = cfgClientIpify{}; return cfg },
			wantErr: true,
		},
		{
			name:    "empty clients.ipify.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Clients.Ipify.Address = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid clients.ipify.address",
			fcfg:    func(cfg appConfig) appConfig { cfg.Clients.Ipify.Address = "-WRONG_IPIFY_ADDRESS-"; return cfg },
			wantErr: true,
		},
		{
			name:    "empty clients.ipify.timeout",
			fcfg:    func(cfg appConfig) appConfig { cfg.Clients.Ipify.Timeout = 0; return cfg },
			wantErr: true,
		},
		{
			name:    "empty db.main.dsn",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Main.DSN = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "empty db.main.timeout_ping",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Main.TimeoutPing = 0; return cfg },
			wantErr: true,
		},
		{
			name:    "empty db.read.dsn",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Read.DSN = ""; return cfg },
			wantErr: true,
		},
		{
			name:    "empty db.read.timeout_ping",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Read.TimeoutPing = 0; return cfg },
			wantErr: true,
		},
		{
			name: "valid config with database disabled and empty DSNs",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Enabled = false
				cfg.DB.Main.DSN = ""
				cfg.DB.Read.DSN = ""

				return cfg
			},
			wantErr: false,
		},
		{
			name: "valid db connection pool zero values (no limit)",
			fcfg: func(cfg appConfig) appConfig {
				cfg.DB.Main.ConnMaxOpen = 0
				cfg.DB.Main.ConnMaxIdleCount = 0
				cfg.DB.Main.ConnMaxIdleTime = 0
				cfg.DB.Main.ConnMaxLifetime = 0

				return cfg
			},
			wantErr: false,
		},
		{
			name:    "invalid negative db.main.conn_max_open",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Main.ConnMaxOpen = -1; return cfg },
			wantErr: true,
		},
		{
			name:    "invalid negative db.main.conn_max_idle_count",
			fcfg:    func(cfg appConfig) appConfig { cfg.DB.Main.ConnMaxIdleCount = -1; return cfg },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt.fcfg(getValidTestConfig())

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
