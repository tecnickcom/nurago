/*
Package config provides configuration bootstrap for Go services built on top of
Viper.

It keeps configuration loading predictable across local development, CI, and
deployment without repeating boilerplate in every application, centralizing:
  - default values
  - local file discovery
  - environment overrides
  - optional file-less configuration via environment data (the "envvar" provider)
  - optional pluggable remote sources via WithRemoteLoader
  - final schema validation

An application integrates by implementing Configuration:
  - SetDefaults(v Viper) to register application-specific defaults
  - Validate() error to enforce final constraints

This is a Viper-based implementation of the configuration model described in:
Nicola Asuni, 2014-09-13, "Software Configuration"
https://technick.net/guides/software/software_configuration/

# Configuration load order

The effective configuration is built in this order (later steps override earlier
ones):
 1. Built-in defaults from this package (log and shutdown settings), plus
    application defaults from SetDefaults.
 2. Local config file (default: config.json) searched in the explicit
    configDir (if provided) first, and then in:
    ./, $HOME/.<cmdName>/, /etc/<cmdName>/
 3. Environment variables for remote source selection:
    <PREFIX>_REMOTECONFIGPROVIDER,
    <PREFIX>_REMOTECONFIGENDPOINT,
    <PREFIX>_REMOTECONFIGPATH,
    <PREFIX>_REMOTECONFIGSECRETKEYRING,
    <PREFIX>_REMOTECONFIGDATA.
 4. Remote configuration loading, when configured:
    - provider "envvar": decodes base64 JSON from REMOTECONFIGDATA
    - any other provider: delegated to the application-supplied RemoteLoaderFunc
    registered with the WithRemoteLoader option
 5. Environment variables are also applied on the final Viper instance, so they
    can override file/remote values (useful for secrets and runtime overrides).
    Only keys registered with a default (via SetDefaults) or present in the
    config file/remote source are candidates for environment overrides: a key
    that has no default and appears nowhere else is not populated from the
    environment. Register a default for every configurable key to make it
    reliably env-overridable.
 6. Validate() is called on the final decoded config struct.

For a complete implementation example, see the Configuration implementation in
examples/service/internal/cli/config.go and the Load call in
examples/service/internal/cli/cli.go
*/
package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// General constants.
const (
	defaultConfigName = "config" // Base name of the file containing the local configuration data.
	defaultConfigType = "json"   // Type and file extension of the file containing the local configuration data.
	providerEnvVar    = "envvar" // Provider name for the environment variable configuration source.
)

// Remote configuration key names.
const (
	keyRemoteConfigProvider      = "remoteConfigProvider"
	keyRemoteConfigEndpoint      = "remoteConfigEndpoint"
	keyRemoteConfigPath          = "remoteConfigPath"
	keyRemoteConfigSecretKeyring = "remoteConfigSecretKeyring" //nolint:gosec
	keyRemoteConfigData          = "remoteConfigData"
)

// Remote configuration default values.
const (
	defaultRemoteConfigProvider      = ""
	defaultRemoteConfigEndpoint      = ""
	defaultRemoteConfigPath          = ""
	defaultRemoteConfigSecretKeyring = ""
	defaultRemoteConfigData          = ""
)

// Logger configuration key names.
const (
	keyLogAddress = "log.address"
	keyLogFormat  = "log.format"
	keyLogLevel   = "log.level"
	keyLogNetwork = "log.network"
)

// Logger configuration default values.
const (
	defaultLogFormat  = "JSON"
	defaultLogLevel   = "DEBUG"
	defaultLogAddress = ""
	defaultLogNetwork = ""
)

// Extra parameters key names.
const (
	keyShutdownTimeout = "shutdown_timeout"
)

// Extra parameters default values.
const (
	defaultShutdownTimeout = 30 // time in seconds to wait on exit for a graceful shutdown.
)

// Sentinel errors returned by Load and its helpers. Callers can match these with
// errors.Is to distinguish configuration failure modes.
var (
	// ErrNilConfiguration is returned when a nil Configuration is passed to Load.
	ErrNilConfiguration = errors.New("configuration must not be nil")

	// ErrLocalConfig indicates a failure while loading the local configuration.
	ErrLocalConfig = errors.New("failed loading local configuration")

	// ErrRemoteConfig indicates a failure while loading the remote configuration.
	ErrRemoteConfig = errors.New("failed loading remote configuration")

	// ErrInvalidRemoteConfig indicates the remote-source selection settings are invalid.
	ErrInvalidRemoteConfig = errors.New("invalid remote source configuration")

	// ErrValidation indicates the final decoded configuration failed validation.
	ErrValidation = errors.New("failed validating configuration")

	// ErrMissingRemoteVar indicates a required remote-provider variable is not set.
	ErrMissingRemoteVar = errors.New("missing required remote configuration variable")
)

// Configuration is the interface we need the application config struct to implement.
type Configuration interface {
	// SetDefaults registers a default value for every configurable key.
	//
	// Registering a default for each key is required for environment-variable
	// overrides to work: viper only applies an environment override to a key it
	// already knows about (via a default, the config file, or a remote source).
	// A key with no default that is absent from the file/remote source will not
	// be populated from the environment. Use an empty value (e.g. "") when there
	// is no meaningful default.
	SetDefaults(v Viper)

	// Validate enforces final constraints on the fully decoded configuration.
	// It is invoked by Load after all sources have been merged.
	Validate() error
}

// Viper is the local interface to the actual viper to allow for mocking.
//
//nolint:interfacebloat
type Viper interface {
	AddConfigPath(in string)
	AllKeys() []string
	AutomaticEnv()
	BindEnv(input ...string) error
	BindPFlag(key string, flag *pflag.Flag) error
	Get(key string) any
	ReadConfig(in io.Reader) error
	ReadInConfig() error
	SetConfigName(in string)
	SetConfigType(in string)
	SetDefault(key string, value any)
	SetEnvKeyReplacer(r *strings.Replacer)
	SetEnvPrefix(in string)
	Unmarshal(rawVal any, opts ...viper.DecoderConfigOption) error
}

// BaseConfig contains the default configuration options to be used in the application config struct.
type BaseConfig struct {
	// Log configuration.
	Log LogConfig `mapstructure:"log" validate:"required"`

	// ShutdownTimeout is the time in seconds to wait for graceful shutdown.
	ShutdownTimeout int64 `mapstructure:"shutdown_timeout" validate:"omitempty,min=1,max=3600"`
}

// LogConfig contains the configuration for the application logger.
type LogConfig struct {
	// Level is the standard syslog level: EMERGENCY, ALERT, CRITICAL, ERROR, WARNING, NOTICE, INFO, DEBUG.
	Level string `mapstructure:"level" validate:"required,oneof=EMERGENCY ALERT CRITICAL ERROR WARNING NOTICE INFO DEBUG"`

	// Format is the log output format: CONSOLE, JSON.
	Format string `mapstructure:"format" validate:"required,oneof=CONSOLE JSON"`

	// Network is the optional network protocol used to send logs via syslog: udp, tcp.
	Network string `mapstructure:"network" validate:"omitempty,oneof=udp tcp"`

	// Address is the optional remote syslog network address: (ip:port) or just (:port).
	Address string `mapstructure:"address" validate:"omitempty,hostname_port"`
}

// RemoteSourceConfig contains the remote source options used to locate and load
// the optional remote configuration. It is passed to the RemoteLoaderFunc
// registered via WithRemoteLoader, which interprets and validates the fields
// it uses.
type RemoteSourceConfig struct {
	// Provider is the optional external configuration source: the built-in
	// "envvar" or any name handled by a loader registered via WithRemoteLoader.
	// When envvar is set the data should be set in the Data field.
	Provider string `mapstructure:"remoteConfigProvider"`

	// Endpoint is the remote configuration URL (ip:port), passed verbatim to the remote loader.
	Endpoint string `mapstructure:"remoteConfigEndpoint"`

	// Path is the remote configuration path where to search for the configuration data (e.g. "/cli/program").
	// This is a path on the remote provider, not on the local filesystem.
	Path string `mapstructure:"remoteConfigPath"`

	// SecretKeyring is the path to an optional secret keyring the remote loader
	// can use to decrypt the remote configuration data (e.g.: "/etc/program/configkey.gpg").
	SecretKeyring string `mapstructure:"remoteConfigSecretKeyring"`

	// Data is the base64 encoded JSON configuration data to be used with the "envvar" provider.
	Data string `mapstructure:"remoteConfigData"`
}

// Load builds cfg from defaults, local config, environment, and optional remote sources.
//
// It applies the documented merge order, unmarshals into cfg, and executes
// cfg.Validate before returning. Optional behaviors (e.g. a custom remote source
// loader) can be enabled via opts.
func Load(cmdName, configDir, envPrefix string, cfg Configuration, opts ...Option) error {
	if cfg == nil {
		return ErrNilConfiguration
	}

	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	localViper := viper.New()
	remoteViper := viper.New()

	return loadConfig(localViper, remoteViper, cmdName, configDir, envPrefix, cfg, o)
}

// loadConfig performs the full configuration pipeline for cfg.
//
// It loads local values, overlays optional remote values, then validates the
// final typed configuration.
func loadConfig(localViper, remoteViper Viper, cmdName, configDir, envPrefix string, cfg Configuration, o *options) error {
	remoteSourceCfg, err := loadLocalConfig(localViper, cmdName, configDir, envPrefix, cfg, o)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLocalConfig, err)
	}

	err = loadRemoteConfig(localViper, remoteViper, remoteSourceCfg, envPrefix, cfg, o)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRemoteConfig, err)
	}

	err = cfg.Validate()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrValidation, err)
	}

	return nil
}

// loadLocalConfig initializes defaults and loads local configuration state.
//
// It configures default values, search paths, environment bindings for remote
// source selection, and reads the local config file into v. It then unmarshals
// only remote-source settings into RemoteSourceConfig so remote loading can be
// resolved in a separate step.
func loadLocalConfig(v Viper, cmdName, configDir, envPrefix string, cfg Configuration, o *options) (*RemoteSourceConfig, error) {
	// set default remote configuration values
	v.SetDefault(keyRemoteConfigProvider, defaultRemoteConfigProvider)
	v.SetDefault(keyRemoteConfigEndpoint, defaultRemoteConfigEndpoint)
	v.SetDefault(keyRemoteConfigPath, defaultRemoteConfigPath)
	v.SetDefault(keyRemoteConfigSecretKeyring, defaultRemoteConfigSecretKeyring)
	v.SetDefault(keyRemoteConfigData, defaultRemoteConfigData)

	// set default logging configuration values
	v.SetDefault(keyLogFormat, defaultLogFormat)
	v.SetDefault(keyLogLevel, defaultLogLevel)
	v.SetDefault(keyLogAddress, defaultLogAddress)
	v.SetDefault(keyLogNetwork, defaultLogNetwork)

	// set default config name and type
	v.SetConfigName(defaultConfigName)
	v.SetConfigType(defaultConfigType)

	// add default search paths
	configureSearchPath(v, cmdName, configDir)

	// set application defaults
	v.SetDefault(keyShutdownTimeout, defaultShutdownTimeout)

	// set defaults from application configuration
	cfg.SetDefaults(v)

	// support environment variables for the remote configuration
	configureEnv(v, envPrefix)

	envVar := []string{
		keyRemoteConfigProvider,
		keyRemoteConfigEndpoint,
		keyRemoteConfigPath,
		keyRemoteConfigSecretKeyring,
		keyRemoteConfigData,
	}

	for _, ev := range envVar {
		_ = v.BindEnv(ev) // we ignore the error because we are always passing an argument value
	}

	// Find and read the local configuration file (if any).
	// A missing local config file is not fatal: configuration can be provided
	// entirely via defaults, environment variables, or a remote provider
	// (e.g. the "envvar" provider for fully file-less deployments).
	err := v.ReadInConfig()
	if err != nil {
		if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
			return nil, fmt.Errorf("failed reading in config: %w", err)
		}
	}

	var rsCfg RemoteSourceConfig

	err = v.Unmarshal(&rsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling config: %w", err)
	}

	// Fail fast on invalid remote-source selection (e.g. an unsupported provider
	// name) so problems surface here with a clear message instead of later as an
	// opaque remote-backend error.
	err = validateRemoteSourceConfig(&rsCfg, o.remoteLoader != nil)
	if err != nil {
		return nil, err
	}

	return &rsCfg, nil
}

// loadRemoteConfig overlays remote-source data and unmarshals into cfg.
//
// It starts from local defaults/values, applies environment overrides, loads
// optional remote configuration depending on provider, and finally unmarshals
// into the application struct.
func loadRemoteConfig(lv Viper, rv Viper, rs *RemoteSourceConfig, envPrefix string, cfg Configuration, o *options) error {
	// Seed the remote viper with every resolved local key as a default. This
	// intentionally demotes local file/default values to the "default" layer so
	// that remote-provider data and environment variables (applied below) take
	// precedence, matching the documented load order. Only keys present here can
	// be overridden via environment variables (see the SetDefaults contract).
	for _, k := range lv.AllKeys() {
		rv.SetDefault(k, lv.Get(k))
	}

	rv.SetConfigType(defaultConfigType)

	// Environment variables take precedence over configuration files.
	// This is useful to populate secret values from environment variables.
	configureEnv(rv, envPrefix)

	var err error

	switch {
	case rs.Provider == "":
		// ignore remote source
	case rs.Provider == providerEnvVar:
		err = loadFromEnvVarSource(rv, rs, envPrefix)
	case o.remoteLoader != nil:
		err = loadFromRemoteLoader(rv, rs, o.remoteLoader)
	default:
		// Unreachable via Load (validateRemoteSourceConfig rejects custom
		// providers when no remote loader is registered); kept as defense in
		// depth for direct callers.
		err = unsupportedProviderError(rs.Provider)
	}

	if err != nil {
		return fmt.Errorf("failed loading configuration from remote source: %w", err)
	}

	err = rv.Unmarshal(cfg)
	if err != nil {
		return fmt.Errorf("failed loading application configuration: %w", err)
	}

	return nil
}

// loadFromEnvVarSource reads base64-encoded JSON configuration from env data.
//
// It supports the providerEnvVar mode, enabling fully file-less deployments
// where configuration is injected through environment variables.
func loadFromEnvVarSource(v Viper, rc *RemoteSourceConfig, envPrefix string) error {
	if rc.Data == "" {
		return validationError(rc.Provider, envPrefix, keyRemoteConfigData)
	}

	data, err := base64.StdEncoding.DecodeString(rc.Data)
	if err != nil {
		return fmt.Errorf("failed decoding config data: %w", err)
	}

	return v.ReadConfig(bytes.NewReader(data)) //nolint:wrapcheck
}

// loadFromRemoteLoader reads configuration from the application-supplied
// remote source loader (see WithRemoteLoader) and merges the returned data
// following the documented precedence rules.
func loadFromRemoteLoader(v Viper, rs *RemoteSourceConfig, loader RemoteLoaderFunc) error {
	data, err := loader(rs)
	if err != nil {
		return fmt.Errorf("the remote loader failed for provider %q: %w", rs.Provider, err)
	}

	if data == nil {
		// A nil reader (with a nil error) means there is no remote data to merge.
		return nil
	}

	if c, ok := data.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}

	return v.ReadConfig(data) //nolint:wrapcheck
}

// configureSearchPath registers local config lookup directories in search order.
//
// If configDir is provided, it is checked first, then standard fallback paths
// are appended.
func configureSearchPath(v Viper, cmdName, configDir string) {
	var configSearchPath []string

	if configDir != "" {
		// add the configuration directory specified as program argument
		configSearchPath = append(configSearchPath, configDir)
	}

	// add default search directories for the configuration file
	configSearchPath = append(configSearchPath, []string{
		"./",
		"$HOME/." + cmdName + "/",
		"/etc/" + cmdName + "/",
	}...)

	for _, p := range configSearchPath {
		v.AddConfigPath(p)
	}
}

// configureEnv wires environment-variable support on v using the shared prefix
// and key-replacer rules, so local and remote viper instances resolve
// environment variables identically.
func configureEnv(v Viper, envPrefix string) {
	v.AutomaticEnv()
	v.SetEnvPrefix(normalizeEnvPrefix(envPrefix)) // will be uppercased automatically
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
}

// normalizeEnvPrefix converts the environment prefix to the canonical form used
// for variable lookups: "-" becomes "_" (viper uppercases it later).
func normalizeEnvPrefix(envPrefix string) string {
	return strings.ReplaceAll(envPrefix, "-", "_")
}

// validateRemoteSourceConfig fails fast on an unsupported remote-source provider.
//
// Only the provider name is checked here because it selects the load path and a
// typo would otherwise silently route to the wrong branch. Data presence and
// base64 encoding are validated in loadFromEnvVarSource with an actionable
// message, while custom providers are interpreted and validated by the remote
// loader registered via WithRemoteLoader. Without a registered loader only the
// package-specific "envvar" provider (or the empty value) is supported.
func validateRemoteSourceConfig(rs *RemoteSourceConfig, hasRemoteLoader bool) error {
	switch rs.Provider {
	case "", providerEnvVar:
		return nil
	default:
		if hasRemoteLoader {
			return nil
		}

		return unsupportedProviderError(rs.Provider)
	}
}

// unsupportedProviderError formats the rejection error for a remote-source
// provider name that is not handled by this package.
func unsupportedProviderError(provider string) error {
	return fmt.Errorf("%w: unsupported remoteConfigProvider %q (must be %q or empty; other providers require a remote loader registered via WithRemoteLoader)", ErrInvalidRemoteConfig, provider, providerEnvVar)
}

// validationError formats a consistent missing-variable error for providers.
//
// It produces an actionable message that includes provider name and the exact
// expected environment variable key, applying the same "-" to "_" replacement
// used when binding environment variables.
func validationError(provider, envPrefix, varName string) error {
	return fmt.Errorf("%w: %s config provider requires %s_%s to be set", ErrMissingRemoteVar, provider, strings.ToUpper(normalizeEnvPrefix(envPrefix)), strings.ToUpper(varName))
}
