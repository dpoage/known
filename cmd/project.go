package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dpoage/known/model"
	"gopkg.in/yaml.v3"
)

// projectConfigFile is the filename for project-local configuration.
const projectConfigFile = ".known.yaml"

// projectConfig holds configuration parsed from a project-local .known.yaml file.
type projectConfig struct {
	DSN               string            `yaml:"dsn"`
	ScopePrefix       string            `yaml:"scope_prefix,omitempty"`
	MaxContentLength  *int              `yaml:"max_content_length,omitempty"`
	SearchThreshold   *float64          `yaml:"search_threshold,omitempty"`
	RecallLimit       *int              `yaml:"recall_limit,omitempty"`
	RecallExpandDepth *int              `yaml:"recall_expand_depth,omitempty"`
	RecallRecency     *float64          `yaml:"recall_recency,omitempty"`
	DefaultTTL        map[string]string `yaml:"default_ttl,omitempty"`
}

// findProjectConfig walks up from startDir looking for a .known.yaml file.
// Returns the file path and its parent directory if found, or empty strings if not.
func findProjectConfig(startDir string) (filePath, dir string) {
	dir = startDir
	for {
		candidate := filepath.Join(dir, projectConfigFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			return "", ""
		}
		dir = parent
	}
}

// loadProjectConfig reads and parses a .known.yaml file.
func loadProjectConfig(filePath string) (*projectConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// deriveScope computes a scope path from the relative position of cwd under scopeRoot.
// Directory names that aren't valid scope segments (e.g., ".hidden", "123build") are
// silently skipped. When prefix is non-empty, it replaces "root" as the base scope:
// the project root returns the prefix itself, and subdirectories return prefix + "." + path.
// Returns "root" when prefix is empty and cwd equals scopeRoot, is outside it, or all
// path segments are invalid.
func deriveScope(scopeRoot, cwd, prefix string) string {
	base := model.RootScope
	if prefix != "" {
		base = prefix
	}

	absRoot, err := filepath.Abs(scopeRoot)
	if err != nil {
		return base
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return base
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return base
	}
	absCwd, err = filepath.EvalSymlinks(absCwd)
	if err != nil {
		return base
	}

	rel, err := filepath.Rel(absRoot, absCwd)
	if err != nil {
		return base
	}
	if rel == "." {
		return base
	}
	if strings.HasPrefix(rel, "..") {
		return base
	}

	parts := strings.Split(rel, string(filepath.Separator))
	var segments []string
	for _, p := range parts {
		if model.IsValidScopeSegment(p) {
			segments = append(segments, p)
		}
	}
	if len(segments) == 0 {
		return base
	}
	relScope := strings.Join(segments, ".")
	if prefix != "" {
		return prefix + "." + relScope
	}
	return relScope
}
