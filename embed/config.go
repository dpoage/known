package embed

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all embedder-related settings.
type Config struct {
	// Embedder selects the backend: "ollama" or "openai-compatible".
	Embedder string

	// Model is the embedding model name (e.g. "nomic-embed-text").
	Model string

	// URL is the base URL for the embedding service.
	URL string

	// APIKey is the bearer token for authenticated APIs (optional for Ollama).
	APIKey string

	// Dimensions overrides the expected vector dimensionality.
	// When zero the embedder auto-detects from the first response.
	Dimensions int

	// CacheEnabled turns on the content-hash embedding cache.
	CacheEnabled bool
}

// defaults returns a Config with sensible local-first defaults.
func defaults() Config {
	return Config{
		Embedder:     "ollama",
		Model:        "nomic-embed-text",
		URL:          "http://localhost:11434",
		CacheEnabled: false,
	}
}

// LoadConfig reads embedder configuration from Viper.
//
// Environment variables (prefix KNOWN_):
//
//	KNOWN_EMBEDDER          - "ollama" or "openai-compatible"
//	KNOWN_EMBED_MODEL       - model name
//	KNOWN_EMBED_URL         - base URL
//	KNOWN_EMBED_API_KEY     - API key / bearer token
//	KNOWN_EMBED_DIMENSIONS  - override vector dimensions
//	KNOWN_EMBED_CACHE       - "true" to enable caching
func LoadConfig() Config {
	viper.SetEnvPrefix("KNOWN")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	cfg := defaults()

	if v := viper.GetString("EMBEDDER"); v != "" {
		cfg.Embedder = v
	}
	if v := viper.GetString("EMBED_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := viper.GetString("EMBED_URL"); v != "" {
		cfg.URL = v
	}
	if v := viper.GetString("EMBED_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := viper.GetInt("EMBED_DIMENSIONS"); v > 0 {
		cfg.Dimensions = v
	}
	if viper.GetBool("EMBED_CACHE") {
		cfg.CacheEnabled = true
	}

	return cfg
}

// Validate checks that the configuration is internally consistent.
func (c Config) Validate() error {
	switch c.Embedder {
	case "ollama", "openai-compatible":
		// ok
	default:
		return fmt.Errorf("unknown embedder type %q", c.Embedder)
	}
	if c.Model == "" {
		return fmt.Errorf("embedding model name is required")
	}
	if c.URL == "" {
		return fmt.Errorf("embedding service URL is required")
	}
	if c.Dimensions < 0 {
		return fmt.Errorf("dimensions must be non-negative, got %d", c.Dimensions)
	}
	return nil
}
