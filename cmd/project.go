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
	DSN              string            `yaml:"dsn"`
	MaxContentLength *int              `yaml:"max_content_length,omitempty"`
	SearchThreshold  *float64          `yaml:"search_threshold,omitempty"`
	DefaultTTL       map[string]string `yaml:"default_ttl,omitempty"`
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
// silently skipped. Returns "root" when cwd equals scopeRoot, is outside it, or all
// path segments are invalid.
func deriveScope(scopeRoot, cwd string) string {
	absRoot, err := filepath.Abs(scopeRoot)
	if err != nil {
		return model.RootScope
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return model.RootScope
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return model.RootScope
	}
	absCwd, err = filepath.EvalSymlinks(absCwd)
	if err != nil {
		return model.RootScope
	}

	rel, err := filepath.Rel(absRoot, absCwd)
	if err != nil {
		return model.RootScope
	}
	if rel == "." {
		return model.RootScope
	}
	if strings.HasPrefix(rel, "..") {
		return model.RootScope
	}

	parts := strings.Split(rel, string(filepath.Separator))
	var segments []string
	for _, p := range parts {
		if model.IsValidScopeSegment(p) {
			segments = append(segments, p)
		}
	}
	if len(segments) == 0 {
		return model.RootScope
	}
	return strings.Join(segments, ".")
}
