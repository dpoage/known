package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// AppConfig holds the resolved configuration for the CLI.
type AppConfig struct {
	DSN   string
	JSON  bool
	Quiet bool
}

// loadAppConfig resolves configuration from flags, environment, and config file.
// Priority: flags > env > config file > defaults.
func loadAppConfig(gf globalFlags) (*AppConfig, error) {
	// Set up Viper for config file.
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(home, ".known"))
	}

	// Read config file if it exists; ignore "not found" errors.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Only fail on actual read errors, not missing file.
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := &AppConfig{
		JSON:  gf.json,
		Quiet: gf.quiet,
	}

	// DSN resolution: flag > env > config file.
	switch {
	case gf.dsn != "":
		cfg.DSN = gf.dsn
	case os.Getenv("KNOWN_DSN") != "":
		cfg.DSN = os.Getenv("KNOWN_DSN")
	default:
		cfg.DSN = viper.GetString("dsn")
	}

	if cfg.DSN == "" {
		return nil, fmt.Errorf("database connection string required: set --dsn flag, KNOWN_DSN env var, or dsn in ~/.known/config.yaml")
	}

	return cfg, nil
}
