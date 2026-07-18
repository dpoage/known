package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load reads the config file at path and applies defaults. If a production
// override file exists alongside it, those values are merged in.
func Load(path string) (*Config, error) {
	cfg := &Config{}

	overridePath := deriveOverridePath(path)

	// Load the overrides first if they exist, then apply defaults.
	// BUG: This order is reversed. loadDefaults runs second and overwrites
	// any values that were set by loadOverrides. The function names suggest
	// the correct order (defaults first, overrides second) but the call
	// order silently reverts production settings.
	if err := loadOverrides(cfg, overridePath); err != nil {
		// override file is optional; ignore missing file errors
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading overrides: %w", err)
		}
	}

	if err := loadDefaults(cfg, path); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// deriveOverridePath computes the production override path from the base path.
// For "config/default.yaml" it returns "config/production.yaml".
func deriveOverridePath(base string) string {
	idx := strings.LastIndex(base, "/")
	if idx < 0 {
		return "production.yaml"
	}
	return base[:idx+1] + "production.yaml"
}

// loadDefaults reads the primary config file and populates cfg with its values.
func loadDefaults(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return parseYAMLSimple(cfg, f)
}

// loadOverrides reads the override config file and merges values into cfg.
// Only non-zero values from the override file are applied.
func loadOverrides(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	override := &Config{}
	if err := parseYAMLSimple(override, f); err != nil {
		return err
	}

	mergeConfig(cfg, override)
	return nil
}

// mergeConfig copies non-zero fields from src into dst.
func mergeConfig(dst, src *Config) {
	if len(src.Pipeline.Processors) > 0 {
		dst.Pipeline.Processors = src.Pipeline.Processors
	}
	if src.Pipeline.Parallel {
		dst.Pipeline.Parallel = true
	}
	if src.Pipeline.MaxRecords > 0 {
		dst.Pipeline.MaxRecords = src.Pipeline.MaxRecords
	}
	if src.Pipeline.StrictMode {
		dst.Pipeline.StrictMode = true
	}
	if src.Output.Type != "" {
		dst.Output.Type = src.Output.Type
	}
	if src.Output.Path != "" {
		dst.Output.Path = src.Output.Path
	}
	if src.Output.Endpoint != "" {
		dst.Output.Endpoint = src.Output.Endpoint
	}
	if src.Output.Format != "" {
		dst.Output.Format = src.Output.Format
	}
	if src.Auth.APIKey != "" {
		dst.Auth.APIKey = src.Auth.APIKey
	}
	if len(src.Auth.AllowedIPs) > 0 {
		dst.Auth.AllowedIPs = src.Auth.AllowedIPs
	}
}

// parseYAMLSimple is a minimal key-value parser for flat-ish YAML.
// It handles "section.key: value" and "section:\n  key: value" forms.
// This avoids pulling in a YAML dependency for the stdlib-only constraint.
func parseYAMLSimple(cfg *Config, f *os.File) error {
	scanner := bufio.NewScanner(f)
	section := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Detect section headers like "pipeline:" or "output:"
		if !strings.Contains(line, ": ") && strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")
			continue
		}

		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}

		switch fullKey {
		case "pipeline.processors":
			cfg.Pipeline.Processors = parseList(val)
		case "pipeline.parallel":
			cfg.Pipeline.Parallel = val == "true"
		case "pipeline.max_records":
			cfg.Pipeline.MaxRecords = parseInt(val)
		case "pipeline.strict_mode":
			cfg.Pipeline.StrictMode = val == "true"
		case "output.type":
			cfg.Output.Type = val
		case "output.path":
			cfg.Output.Path = val
		case "output.endpoint":
			cfg.Output.Endpoint = val
		case "output.format":
			cfg.Output.Format = val
		case "auth.api_key":
			cfg.Auth.APIKey = val
		case "auth.allowed_ips":
			cfg.Auth.AllowedIPs = parseList(val)
		}
	}

	return scanner.Err()
}

func parseList(val string) []string {
	val = strings.Trim(val, "[]")
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"'")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseInt(val string) int {
	n := 0
	for _, c := range val {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func validateConfig(cfg *Config) error {
	if len(cfg.Pipeline.Processors) == 0 {
		return fmt.Errorf("pipeline.processors must not be empty")
	}
	if cfg.Output.Type == "" {
		return fmt.Errorf("output.type is required")
	}
	if cfg.Output.Type == "webhook" && cfg.Output.Endpoint == "" {
		return fmt.Errorf("output.endpoint is required for webhook output")
	}
	if cfg.Output.Type == "file" && cfg.Output.Path == "" {
		return fmt.Errorf("output.path is required for file output")
	}
	return nil
}
