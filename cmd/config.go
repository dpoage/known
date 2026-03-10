package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/spf13/viper"
)

// AppConfig holds the resolved configuration for the CLI.
type AppConfig struct {
	DSN               string
	JSON              bool
	Quiet             bool
	MaxContentLength  int                                // default 4096
	SearchThreshold   float64                            // default 0.4
	RecallLimit       int                                // default 5
	RecallExpandDepth int                                // default 0
	RecallRecency     float64                            // default 0.1
	DefaultTTL        map[model.SourceType]time.Duration // source type -> auto-TTL
	ScopeRoot         string                             // directory containing .known.yaml (or from global scope_root)
	ScopePrefix       string                             // project scope prefix from .known.yaml
	DefaultScope      string                             // auto-derived scope from cwd relative to ScopeRoot
}

// QualifyScope prepends the project's scope prefix to a user-provided scope value.
// Empty input returns empty. A leading "/" bypasses qualification (literal/cross-project).
// "root" maps to the prefix itself. Already-qualified values are returned unchanged.
func (c *AppConfig) QualifyScope(scope string) string {
	if scope == "" {
		return scope
	}
	// Leading "/" = literal (cross-project) scope.
	if strings.HasPrefix(scope, "/") {
		return scope[1:]
	}
	if c.ScopePrefix == "" {
		return scope
	}
	// Already qualified — don't double-prefix.
	if scope == c.ScopePrefix || strings.HasPrefix(scope, c.ScopePrefix+".") {
		return scope
	}
	if scope == model.RootScope {
		return c.ScopePrefix
	}
	return c.ScopePrefix + "." + scope
}

// loadAppConfig resolves configuration from flags, environment, project config,
// and global config file.
//
// Resolution priority:
//   - DSN: flag > env > project .known.yaml > global ~/.known/config.yaml > error
//   - Scope default: explicit --scope flag > auto-derived from cwd > "root"
//   - Other config: project .known.yaml > global config > hardcoded defaults
func loadAppConfig(gf globalFlags) (*AppConfig, error) {
	cfg := &AppConfig{
		JSON:  gf.json,
		Quiet: gf.quiet,
	}

	// 1. Discover project-local .known.yaml by walking up from cwd.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	var projCfg *projectConfig
	projFile, projDir := findProjectConfig(cwd)
	if projFile != "" {
		pc, err := loadProjectConfig(projFile)
		if err != nil {
			return nil, fmt.Errorf("read project config %s: %w", projFile, err)
		}
		projCfg = pc
		cfg.ScopeRoot = projDir
	}

	// 2. Load global config via Viper.
	loadGlobalConfig()

	// 3. DSN resolution: flag > env > project .known.yaml > viper global > error.
	switch {
	case gf.dsn != "":
		cfg.DSN = gf.dsn
	case os.Getenv("KNOWN_DSN") != "":
		cfg.DSN = os.Getenv("KNOWN_DSN")
	case projCfg != nil && projCfg.DSN != "":
		cfg.DSN = projCfg.DSN
	default:
		cfg.DSN = viper.GetString("dsn")
	}

	if cfg.DSN == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("database connection string required and cannot determine home directory: %w", err)
		}
		knownDir := filepath.Join(home, ".known")
		if err := os.MkdirAll(knownDir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", knownDir, err)
		}
		cfg.DSN = filepath.Join(knownDir, "known.db")
	}

	// 4. ScopeRoot: project dir > global scope_root > (absent).
	if cfg.ScopeRoot == "" {
		if sr := viper.GetString("scope_root"); sr != "" {
			cfg.ScopeRoot = expandHome(sr)
		}
	}

	// 5. ScopePrefix + DefaultScope.
	if projCfg != nil {
		cfg.ScopePrefix = projCfg.ScopePrefix
	}
	if cfg.ScopeRoot != "" {
		cfg.DefaultScope = deriveScope(cfg.ScopeRoot, cwd, cfg.ScopePrefix)
	} else {
		cfg.DefaultScope = model.RootScope
	}

	// 6. MaxContentLength: project > global > hardcoded default.
	cfg.MaxContentLength = model.MaxContentLength
	if projCfg != nil && projCfg.MaxContentLength != nil {
		cfg.MaxContentLength = *projCfg.MaxContentLength
	} else if v := viper.GetInt("max_content_length"); v > 0 {
		cfg.MaxContentLength = v
	}

	// 7. SearchThreshold: project > global > hardcoded default.
	cfg.SearchThreshold = 0.4
	if projCfg != nil && projCfg.SearchThreshold != nil {
		cfg.SearchThreshold = *projCfg.SearchThreshold
	} else if viper.IsSet("search_threshold") {
		cfg.SearchThreshold = viper.GetFloat64("search_threshold")
	}

	// 8. RecallLimit: project > global > hardcoded default.
	cfg.RecallLimit = 5
	if projCfg != nil && projCfg.RecallLimit != nil {
		cfg.RecallLimit = *projCfg.RecallLimit
	} else if viper.IsSet("recall_limit") {
		cfg.RecallLimit = viper.GetInt("recall_limit")
	}

	// 9. RecallExpandDepth: project > global > hardcoded default.
	cfg.RecallExpandDepth = 0
	if projCfg != nil && projCfg.RecallExpandDepth != nil {
		cfg.RecallExpandDepth = *projCfg.RecallExpandDepth
	} else if viper.IsSet("recall_expand_depth") {
		cfg.RecallExpandDepth = viper.GetInt("recall_expand_depth")
	}

	// 10. RecallRecency: project > global > hardcoded default.
	cfg.RecallRecency = 0.1
	if projCfg != nil && projCfg.RecallRecency != nil {
		cfg.RecallRecency = *projCfg.RecallRecency
	} else if viper.IsSet("recall_recency") {
		cfg.RecallRecency = viper.GetFloat64("recall_recency")
	}

	// 11. DefaultTTL: hardcoded defaults, overridable by project then global.
	cfg.DefaultTTL = map[model.SourceType]time.Duration{
		model.SourceConversation: 7 * 24 * time.Hour,  // 7 days
		model.SourceManual:       90 * 24 * time.Hour, // 90 days
	}
	// Override from global config (e.g., default_ttl.conversation: 168h).
	for _, st := range []model.SourceType{model.SourceFile, model.SourceURL, model.SourceConversation, model.SourceManual} {
		key := "default_ttl." + string(st)
		if viper.IsSet(key) {
			d, err := time.ParseDuration(viper.GetString(key))
			if err != nil {
				return nil, fmt.Errorf("invalid default_ttl.%s: %w", st, err)
			}
			cfg.DefaultTTL[st] = d
		}
	}
	// Override from project config (takes precedence over global).
	if projCfg != nil {
		for stKey, durStr := range projCfg.DefaultTTL {
			d, err := time.ParseDuration(durStr)
			if err != nil {
				return nil, fmt.Errorf("invalid project default_ttl.%s: %w", stKey, err)
			}
			cfg.DefaultTTL[model.SourceType(stKey)] = d
		}
	}

	return cfg, nil
}

// loadGlobalConfig sets up Viper to read ~/.known/config.yaml.
func loadGlobalConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(home, ".known"))
	}

	// Read config file if it exists; ignore "not found" errors.
	if err := viper.ReadInConfig(); err != nil {
		// Silently ignore missing config file.
		return
	}
}

// expandHome replaces a leading "~/" with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
