package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dpoage/known/model"
	"github.com/spf13/viper"
)

// AppConfig holds the resolved configuration for the CLI.
type AppConfig struct {
	DSN               string
	JSON              bool
	Quiet             bool
	MaxContentLength  int     // default 4096
	SearchThreshold   float64 // default 0.4
	RecallLimit       int     // default 5
	RecallExpandDepth int     // default 0
	RecallRecency     float64 // default 0.1
	// DefaultTTL is intentionally absent: TTL is opt-in via --ttl flag.
	// Unflagged adds are permanent (no ExpiresAt set).
	ScopeRoot    string // project root dir: .known.yaml dir, marker-derived, or global scope_root
	ScopePrefix  string // scope prefix: .known.yaml scope_prefix, else sanitized marker-root dir name
	DefaultScope string // auto-derived scope from cwd relative to ScopeRoot
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
//   - DSN: flag > env > project .known.yaml > global ~/.known/config.yaml > default ~/.known/known.db
//   - ScopePrefix: .known.yaml scope_prefix > sanitizeScopePrefix(marker-root dir name)
//   - ScopeRoot: .known.yaml dir > marker-derived root > global scope_root
//   - DefaultScope: derived from cwd relative to ScopeRoot with ScopePrefix
//   - Other config: project .known.yaml > global config > hardcoded defaults
//
// .known.yaml is entirely optional. When absent (or present but without
// scope_prefix), scope is derived by walking parent directories for VCS/.git
// (highest priority) or build-system manifests (go.mod, Cargo.toml, etc.),
// then sanitizing the root directory name into a valid scope segment
// (invalid chars → '-', leading digits stripped). Explicit flags always win;
// .known.yaml scope_prefix overrides the marker-derived name.
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

	// 3. DSN resolution: flag > env > project .known.yaml > viper global > default.
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

	// 4. ScopeRoot: .known.yaml dir (already set above) > global scope_root.
	if cfg.ScopeRoot == "" {
		if sr := viper.GetString("scope_root"); sr != "" {
			cfg.ScopeRoot = expandHome(sr)
		}
	}

	// 5. ScopePrefix + marker-derived ScopeRoot.
	//
	// Precedence: .known.yaml scope_prefix > sanitized dir name of ScopeRoot.
	// The source of ScopeRoot determines whether a prefix is auto-derived:
	//
	//   a) .known.yaml present and has scope_prefix → use it (explicit override).
	//   b) .known.yaml present but NO scope_prefix → sanitize base(yaml dir).
	//      A DSN-only .known.yaml must not silently collapse to "root"; the
	//      prefix comes from the yaml's own directory name.
	//   c) Global scope_root (from ~/.known/config.yaml) → NO auto-derived
	//      prefix. This preserves main-branch behaviour: container-style global
	//      roots supply their own scope via the existing scope hierarchy; adding
	//      a prefix from base(scope_root) would silently re-scope existing data.
	//   d) No ScopeRoot yet → walk for project markers, set ScopeRoot from the
	//      found root, and sanitize its base name as prefix.
	switch {
	case projCfg != nil && projCfg.ScopePrefix != "":
		// (a) Explicit override in .known.yaml wins outright.
		cfg.ScopePrefix = projCfg.ScopePrefix
	case projCfg != nil:
		// (b) .known.yaml present but no scope_prefix: derive from yaml dir.
		cfg.ScopePrefix = sanitizeScopePrefix(filepath.Base(cfg.ScopeRoot))
	case cfg.ScopeRoot != "":
		// (c) Global scope_root: preserve empty prefix (no auto-derivation).
		// cfg.ScopePrefix remains "".
	default:
		// (d) No .known.yaml, no global scope_root: marker walk + sanitize.
		markerRoot, _ := findProjectRoot(cwd)
		cfg.ScopeRoot = markerRoot
		cfg.ScopePrefix = sanitizeScopePrefix(filepath.Base(markerRoot))
	}

	// 6. DefaultScope from cwd relative to ScopeRoot.
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

	// DefaultTTL removed: TTL is strictly opt-in via --ttl. No auto-application.

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
