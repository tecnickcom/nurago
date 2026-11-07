package cli

import (
	"github.com/tecnickcom/nurago/pkg/config"
	"github.com/tecnickcom/nurago/pkg/validator"
)

const (
	// AppName is the name of the application executable.
	AppName = "nuragoexample"

	// appEnvPrefix is the prefix of the configuration environment variables.
	appEnvPrefix = "NURAGOEXAMPLE"

	// appShortDesc is the short description of the application.
	appShortDesc = "nuragoexampleshortdesc"

	// appLongDesc is the long description of the application.
	appLongDesc = "nuragoexamplelongdesc"

	// fieldTagName is the name of the tag containing the original JSON field name.
	fieldTagName = "mapstructure"
)

const (
	dbDriver = "mysql"
)

// validatorNewFn defines the validator constructor and can be overwritten for testing.
var validatorNewFn = validator.New //nolint:gochecknoglobals

type cfgServer struct {
	Address string `mapstructure:"address" validate:"required,hostname_port"`
	Timeout int    `mapstructure:"timeout" validate:"required,min=1"`
}

type cfgServerMonitoring cfgServer

type cfgServerPrivate cfgServer

type cfgServerPublic cfgServer

// cfgServerGRPC contains the configuration of the gRPC server.
// TLS is enabled when both the certificate and the key files are set.
type cfgServerGRPC struct {
	Enabled     bool   `mapstructure:"enabled"`
	Address     string `mapstructure:"address"       validate:"required,hostname_port"`
	Timeout     int    `mapstructure:"timeout"       validate:"required,min=1"`
	Reflection  bool   `mapstructure:"reflection"`
	TLSCertFile string `mapstructure:"tls_cert_file" validate:"required_with=TLSKeyFile,omitempty,file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"  validate:"required_with=TLSCertFile,omitempty,file"`
}

// cfgServers contains the configuration for all exposed servers.
type cfgServers struct {
	Monitoring cfgServerMonitoring `mapstructure:"monitoring" validate:"required"`
	Private    cfgServerPrivate    `mapstructure:"private"    validate:"required"`
	Public     cfgServerPublic     `mapstructure:"public"     validate:"required"`
	// GRPC is validated conditionally in Validate (only when
	// servers.grpc.enabled is true), so the "-" tag skips the default dive here.
	GRPC cfgServerGRPC `mapstructure:"grpc" validate:"-"`
}

type cfgClientIpify struct {
	Address string `mapstructure:"address" validate:"required,url"`
	Timeout int    `mapstructure:"timeout" validate:"required,min=1"`
}

// cfgClients contains the configuration for all external clients.
type cfgClients struct {
	Ipify cfgClientIpify `mapstructure:"ipify" validate:"required"`
}

type cfgDB struct {
	Driver           string `mapstructure:"driver"              validate:"-"`
	DSN              string `mapstructure:"dsn"                 validate:"required"`
	TimeoutPing      int    `mapstructure:"timeout_ping"        validate:"required"`
	ConnMaxOpen      int    `mapstructure:"conn_max_open"       validate:"min=0"`
	ConnMaxIdleCount int    `mapstructure:"conn_max_idle_count" validate:"min=0"`
	ConnMaxIdleTime  int    `mapstructure:"conn_max_idle_time"  validate:"min=0"`
	ConnMaxLifetime  int    `mapstructure:"conn_max_lifetime"   validate:"min=0"`
}

type cfgDatabases struct {
	Enabled bool  `mapstructure:"enabled"`
	Main    cfgDB `mapstructure:"main"    validate:"required_if=Enabled true"`
	Read    cfgDB `mapstructure:"read"    validate:"required_if=Enabled true"`
}

// appConfig contains the full application configuration.
type appConfig struct {
	config.BaseConfig `mapstructure:",squash" validate:"required"`

	Enabled bool       `mapstructure:"enabled"`
	Servers cfgServers `mapstructure:"servers" validate:"required"`
	Clients cfgClients `mapstructure:"clients" validate:"required"`
	// DB is validated conditionally in Validate (only when db.enabled is true),
	// so the "-" tag skips the default dive here.
	DB cfgDatabases `mapstructure:"db" validate:"-"`
}

// SetDefaults registers baseline configuration values used when no explicit
// value is provided by files, remote providers, or environment variables.
//
// It sets defaults for server endpoints, timeouts, external clients, and
// database pools.
func (c *appConfig) SetDefaults(v config.Viper) {
	v.SetDefault("enabled", true)

	v.SetDefault("servers.monitoring.address", ":8071")
	v.SetDefault("servers.monitoring.timeout", 60)

	v.SetDefault("servers.private.address", ":8072")
	v.SetDefault("servers.private.timeout", 60)

	v.SetDefault("servers.public.address", ":8073")
	v.SetDefault("servers.public.timeout", 60)

	v.SetDefault("servers.grpc.enabled", false)
	v.SetDefault("servers.grpc.address", ":8074")
	v.SetDefault("servers.grpc.timeout", 60)
	v.SetDefault("servers.grpc.reflection", false)
	v.SetDefault("servers.grpc.tls_cert_file", "")
	v.SetDefault("servers.grpc.tls_key_file", "")

	v.SetDefault("clients.ipify.address", "https://api.ipify.org")
	v.SetDefault("clients.ipify.timeout", 1)

	v.SetDefault("db.enabled", false)

	v.SetDefault("db.main.conn_max_idle_count", 5)
	v.SetDefault("db.main.conn_max_idle_time", 60)
	v.SetDefault("db.main.conn_max_lifetime", 3600)
	v.SetDefault("db.main.conn_max_open", 50)
	v.SetDefault("db.main.driver", dbDriver)
	v.SetDefault("db.main.dsn", "")
	v.SetDefault("db.main.timeout_ping", 1)

	v.SetDefault("db.read.conn_max_idle_count", 5)
	v.SetDefault("db.read.conn_max_idle_time", 60)
	v.SetDefault("db.read.conn_max_lifetime", 3600)
	v.SetDefault("db.read.conn_max_open", 50)
	v.SetDefault("db.read.driver", dbDriver)
	v.SetDefault("db.read.dsn", "")
	v.SetDefault("db.read.timeout_ping", 1)
}

// Validate checks the loaded configuration against structural and semantic
// rules before the service starts.
//
// It catches invalid addresses, missing required fields, and unsupported
// values, producing field-aware validation errors tied to config keys.
func (c *appConfig) Validate() error {
	opts := []validator.Option{
		validator.WithFieldNameTag(fieldTagName),
		validator.WithCustomValidationTags(validator.CustomValidationTags()),
		validator.WithErrorTemplates(validator.ErrorTemplates()),
	}

	v, err := validatorNewFn(opts...)
	if err != nil {
		return err
	}

	err = v.ValidateStruct(c)
	if err != nil {
		return err //nolint:wrapcheck
	}

	// The gRPC server configuration is validated only when enabled.
	if c.Servers.GRPC.Enabled {
		err = v.ValidateStruct(&c.Servers.GRPC)
		if err != nil {
			return err //nolint:wrapcheck
		}
	}

	// The database configuration is validated only when enabled, so a service
	// that does not use a database does not need to provide DSNs or pool sizes.
	if c.DB.Enabled {
		return v.ValidateStruct(&c.DB) //nolint:wrapcheck
	}

	return nil
}
