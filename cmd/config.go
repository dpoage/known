package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds CLI configuration values loaded from file, environment,
// and command-line flags.
type Config struct {
	// DSN is the PostgreSQL connection string.
	DSN string

	// JSON enables JSON output mode.
	JSON bool

	// Quiet enables minimal output mode (IDs only).
	Quiet bool
}

// DefaultDSN is the fallback database connection string when none is configured.
const DefaultDSN = "postgres://localhost:5432/known?sslmode=disable"

// LoadConfig reads configuration from ~/.known/config.yaml and environment
// variables (KNOWN_DSN, etc.). Command-line flags override both.
func LoadConfig() Config {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Config directory: ~/.known/
	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(home, ".known"))
	}

	viper.SetEnvPrefix("KNOWN")
	viper.AutomaticEnv()

	viper.SetDefault("dsn", DefaultDSN)
	viper.SetDefault("json", false)
	viper.SetDefault("quiet", false)

	// Ignore missing config file; env and flags still work.
	_ = viper.ReadInConfig()

	return Config{
		DSN:   viper.GetString("dsn"),
		JSON:  viper.GetBool("json"),
		Quiet: viper.GetBool("quiet"),
	}
}
