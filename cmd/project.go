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
	DSN               string   `yaml:"dsn"`
	ScopePrefix       string   `yaml:"scope_prefix,omitempty"`
	MaxContentLength  *int     `yaml:"max_content_length,omitempty"`
	SearchThreshold   *float64 `yaml:"search_threshold,omitempty"`
	RecallLimit       *int     `yaml:"recall_limit,omitempty"`
	RecallExpandDepth *int     `yaml:"recall_expand_depth,omitempty"`
	RecallRecency     *float64 `yaml:"recall_recency,omitempty"`
}

// rootMarkers lists the file/directory names that identify a project root.
//
// # Marker detection precedence
//
// Walking from cwd upward (stopping at $HOME and filesystem root), the search
// uses a two-tier priority scheme:
//
//  1. VCS roots (.git as a directory or file): strongest signal. The walk stops
//     at the first ancestor that contains a .git entry and declares it the root.
//     Git worktrees use a .git *file* pointing to the real repo, so we accept
//     either form. .git beats every build-system marker at any depth because VCS
//     boundaries are the definitive unit of a project.
//
//  2. Build-system manifests (the remainder of this list): weaker signal. These
//     are only consulted when no .git is found in any ancestor. Among them the
//     nearest ancestor wins (i.e., the highest point on the path from cwd toward
//     the root). Rationale: a monorepo may have a top-level go.mod AND a nested
//     package.json; the nearest manifest is the most-specific project identity.
//
// Fallback: when neither a VCS root nor any manifest is found, findProjectRoot
// returns the cwd itself (dir name becomes the scope prefix).
var rootMarkers = []string{
	// VCS — handled separately in findProjectRoot (tier 1).
	".git",
	// Build-system manifests — tier 2, nearest wins.
	"go.mod",
	"Cargo.toml",
	"package.json",
	"pyproject.toml",
	"requirements.txt",
	"CMakeLists.txt",
	"Makefile",
	"BUILD",
	"BUILD.bazel",
}

// findProjectRoot walks from startDir toward the filesystem root, stopping at
// $HOME, looking for a project root directory. See rootMarkers for the
// full precedence definition.
//
// Returns the absolute path of the root directory and a boolean indicating
// whether a marker was found. When no marker is found, startDir itself is
// returned as the fallback root (and ok is false).
func findProjectRoot(startDir string) (root string, ok bool) {
	home, _ := os.UserHomeDir()

	absStart, err := filepath.Abs(startDir)
	if err != nil {
		return startDir, false
	}

	// Tier-1 pass: walk up looking for .git. Stop at $HOME.
	dir := absStart
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		if dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Filesystem root.
			break
		}
		dir = parent
	}

	// Tier-2 pass: build-system manifests. Walk from cwd upward; the first
	// directory that contains any manifest wins (nearest to cwd). Rationale:
	// a nested go.mod is the more-specific project identity than an outer
	// Makefile in a multi-language tree.
	manifests := rootMarkers[1:] // skip ".git"
	dir = absStart
	for {
		for _, m := range manifests {
			if _, err := os.Lstat(filepath.Join(dir, m)); err == nil {
				return dir, true
			}
		}
		if dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// No marker found — fall back to startDir.
	return absStart, false
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

// sanitizeScopePrefix converts a directory name into a valid scope segment.
// Transformation rules (applied in order):
//  1. Any character that is not alphanumeric, '-', or '_' is replaced with '-'.
//     This handles dotted names like "my.app.v1" → "my-app-v1" and version
//     suffixes like "project@2.0" → "project-2-0".
//  2. Leading characters that are not ASCII letters are stripped. This handles
//     digit-first names like "2048game" → "game", or "-foo" → "foo".
//  3. Trailing '-' or '_' are stripped.
//  4. If the result is empty or still fails model.IsValidScopeSegment, returns "".
//     Callers that receive "" should fall back to model.RootScope.
func sanitizeScopePrefix(name string) string {
	if name == "" {
		return ""
	}

	// Step 1: replace invalid chars with '-'.
	b := make([]byte, 0, len(name))
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			b = append(b, c)
		} else {
			b = append(b, '-')
		}
	}

	// Step 2: strip leading non-letter characters.
	for len(b) > 0 && !((b[0] >= 'a' && b[0] <= 'z') || (b[0] >= 'A' && b[0] <= 'Z')) {
		b = b[1:]
	}

	// Step 3: strip trailing separators.
	for len(b) > 0 && (b[len(b)-1] == '-' || b[len(b)-1] == '_') {
		b = b[:len(b)-1]
	}

	result := string(b)
	if !model.IsValidScopeSegment(result) {
		return ""
	}
	return result
}
