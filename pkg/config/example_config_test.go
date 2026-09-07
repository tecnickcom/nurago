package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tecnickcom/nurago/pkg/config"
)

// appConfig is the application configuration struct. Embedding BaseConfig
// brings in the shared log and shutdown settings.
type appConfig struct {
	config.BaseConfig `mapstructure:",squash"`

	ServerAddress string `mapstructure:"server_address" validate:"required"`
	MaxWorkers    int    `mapstructure:"max_workers"    validate:"min=1,max=64"`
}

// SetDefaults registers a default for every key. A key with no default is
// never populated from the environment, so empty values are registered
// explicitly rather than omitted.
func (c *appConfig) SetDefaults(v config.Viper) {
	v.SetDefault("server_address", ":8080")
	v.SetDefault("max_workers", 4)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("shutdown_timeout", 30)
}

// Validate runs after every source has been merged.
func (c *appConfig) Validate() error {
	if c.MaxWorkers > 64 {
		return errors.New("max_workers must not exceed 64")
	}

	return nil
}

func ExampleLoad() {
	// A real service ships config.json alongside the binary, or relies on
	// the search path: ./, $HOME/.<cmdName>/, /etc/<cmdName>/.
	dir, err := os.MkdirTemp("", "nurago-config-example")
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = os.RemoveAll(dir) }()

	file := `{"server_address":":9090","max_workers":8}`

	err = os.WriteFile(filepath.Join(dir, "config.json"), []byte(file), 0o600)
	if err != nil {
		fmt.Println(err)

		return
	}

	// Environment variables override the file, which overrides the
	// defaults. The prefix keeps the service's variables namespaced.
	_ = os.Setenv("EXAMPLESRV_MAX_WORKERS", "16")

	defer func() { _ = os.Unsetenv("EXAMPLESRV_MAX_WORKERS") }()

	cfg := &appConfig{}

	err = config.Load("examplesrv", dir, "EXAMPLESRV", cfg)
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(cfg.ServerAddress, cfg.MaxWorkers, cfg.Log.Level)

	// Output:
	// :9090 16 info
}
