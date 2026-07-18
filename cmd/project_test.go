package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveScope(t *testing.T) {
	// Use a real temp dir as the scope root so EvalSymlinks succeeds.
	tmpRoot := t.TempDir()

	// Create subdirectories that the test cases reference.
	subdirs := []string{
		"personal/known/cmd",
		".hidden/pkg",
		"123build/src",
		"my-project",
		".git",
	}
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(tmpRoot, d), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	tests := []struct {
		name      string
		scopeRoot string
		cwd       string
		prefix    string
		want      string
	}{
		{
			name:      "same directory",
			scopeRoot: tmpRoot,
			cwd:       tmpRoot,
			want:      "root",
		},
		{
			name:      "nested subdirectory",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, "personal/known/cmd"),
			want:      "personal.known.cmd",
		},
		{
			name:      "hidden dir filtered",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, ".hidden/pkg"),
			want:      "pkg",
		},
		{
			name:      "numeric-start dir filtered",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, "123build/src"),
			want:      "src",
		},
		{
			name:      "cwd outside scope root",
			scopeRoot: tmpRoot,
			cwd:       filepath.Dir(tmpRoot),
			want:      "root",
		},
		{
			name:      "hyphenated dir name",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, "my-project"),
			want:      "my-project",
		},
		{
			name:      "dot-prefixed dir only",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, ".git"),
			want:      "root",
		},
		// --- prefix tests ---
		{
			name:      "prefix at root returns prefix",
			scopeRoot: tmpRoot,
			cwd:       tmpRoot,
			prefix:    "myproject",
			want:      "myproject",
		},
		{
			name:      "prefix at subdir returns prefix.subdir",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, "personal/known/cmd"),
			prefix:    "myproject",
			want:      "myproject.personal.known.cmd",
		},
		{
			name:      "prefix with cwd outside scope root returns prefix",
			scopeRoot: tmpRoot,
			cwd:       filepath.Dir(tmpRoot),
			prefix:    "myproject",
			want:      "myproject",
		},
		{
			name:      "prefix with hidden dir filtered",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, ".hidden/pkg"),
			prefix:    "myproject",
			want:      "myproject.pkg",
		},
		{
			name:      "prefix with all-invalid segments returns prefix",
			scopeRoot: tmpRoot,
			cwd:       filepath.Join(tmpRoot, ".git"),
			prefix:    "myproject",
			want:      "myproject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveScope(tt.scopeRoot, tt.cwd, tt.prefix)
			if got != tt.want {
				t.Errorf("deriveScope(%q, %q, %q) = %q, want %q", tt.scopeRoot, tt.cwd, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestFindProjectConfig(t *testing.T) {
	t.Run("found at ancestor", func(t *testing.T) {
		tmp := t.TempDir()
		// Create .known.yaml at root.
		cfgPath := filepath.Join(tmp, projectConfigFile)
		if err := os.WriteFile(cfgPath, []byte("dsn: postgres://localhost/test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Create a nested directory.
		nested := filepath.Join(tmp, "a", "b", "c")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		gotPath, gotDir := findProjectConfig(nested)
		if gotPath != cfgPath {
			t.Errorf("findProjectConfig path = %q, want %q", gotPath, cfgPath)
		}
		if gotDir != tmp {
			t.Errorf("findProjectConfig dir = %q, want %q", gotDir, tmp)
		}
	})

	t.Run("found in current dir", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, projectConfigFile)
		if err := os.WriteFile(cfgPath, []byte("dsn: postgres://localhost/test\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		gotPath, gotDir := findProjectConfig(tmp)
		if gotPath != cfgPath {
			t.Errorf("findProjectConfig path = %q, want %q", gotPath, cfgPath)
		}
		if gotDir != tmp {
			t.Errorf("findProjectConfig dir = %q, want %q", gotDir, tmp)
		}
	})

	t.Run("not found", func(t *testing.T) {
		tmp := t.TempDir()
		nested := filepath.Join(tmp, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		gotPath, gotDir := findProjectConfig(nested)
		if gotPath != "" || gotDir != "" {
			t.Errorf("findProjectConfig should return empty strings, got (%q, %q)", gotPath, gotDir)
		}
	})
}

func TestLoadProjectConfig(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, projectConfigFile)
		content := `dsn: postgres://localhost/test
max_content_length: 8192
search_threshold: 0.5
recall_limit: 10
recall_expand_depth: 2
recall_recency: 0.3
default_ttl:
  conversation: 168h
  manual: 720h
`
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := loadProjectConfig(cfgPath)
		if err != nil {
			t.Fatalf("loadProjectConfig: %v", err)
		}
		if cfg.DSN != "postgres://localhost/test" {
			t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://localhost/test")
		}
		if cfg.MaxContentLength == nil || *cfg.MaxContentLength != 8192 {
			t.Errorf("MaxContentLength = %v, want 8192", cfg.MaxContentLength)
		}
		if cfg.SearchThreshold == nil || *cfg.SearchThreshold != 0.5 {
			t.Errorf("SearchThreshold = %v, want 0.5", cfg.SearchThreshold)
		}
		if cfg.RecallLimit == nil || *cfg.RecallLimit != 10 {
			t.Errorf("RecallLimit = %v, want 10", cfg.RecallLimit)
		}
		if cfg.RecallExpandDepth == nil || *cfg.RecallExpandDepth != 2 {
			t.Errorf("RecallExpandDepth = %v, want 2", cfg.RecallExpandDepth)
		}
		if cfg.RecallRecency == nil || *cfg.RecallRecency != 0.3 {
			t.Errorf("RecallRecency = %v, want 0.3", cfg.RecallRecency)
		}
	})

	t.Run("minimal config", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, projectConfigFile)
		if err := os.WriteFile(cfgPath, []byte("dsn: postgres://localhost/test\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := loadProjectConfig(cfgPath)
		if err != nil {
			t.Fatalf("loadProjectConfig: %v", err)
		}
		if cfg.DSN != "postgres://localhost/test" {
			t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://localhost/test")
		}
		if cfg.MaxContentLength != nil {
			t.Errorf("MaxContentLength should be nil, got %v", cfg.MaxContentLength)
		}
		if cfg.SearchThreshold != nil {
			t.Errorf("SearchThreshold should be nil, got %v", cfg.SearchThreshold)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := loadProjectConfig("/nonexistent/.known.yaml")
		if err == nil {
			t.Error("loadProjectConfig should error for missing file")
		}
	})
}
func TestFindProjectRoot(t *testing.T) {
	// Suppress HOME to prevent actual home directory from affecting tests.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	t.Run("git dir at cwd", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(root)
		if !ok {
			t.Error("expected ok=true for .git dir")
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
	})

	t.Run("git dir in ancestor", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		sub := filepath.Join(root, "pkg", "auth")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(sub)
		if !ok {
			t.Error("expected ok=true")
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
	})

	t.Run("git file (worktree) in ancestor", func(t *testing.T) {
		root := t.TempDir()
		// Git worktrees use a .git file, not a directory.
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../.git/worktrees/wt"), 0o644); err != nil {
			t.Fatal(err)
		}
		sub := filepath.Join(root, "cmd")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(sub)
		if !ok {
			t.Error("expected ok=true for .git file")
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
	})

	t.Run("go.mod beats no marker", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/foo\ngo 1.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		sub := filepath.Join(root, "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(sub)
		if !ok {
			t.Error("expected ok=true for go.mod")
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
	})

	t.Run("git wins over deeper go.mod", func(t *testing.T) {
		// Structure: gitroot/.git, gitroot/sub/go.mod, cwd=gitroot/sub/pkg
		// .git is farther from cwd but git tier-1 wins.
		gitroot := t.TempDir()
		if err := os.Mkdir(filepath.Join(gitroot, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		submod := filepath.Join(gitroot, "sub")
		if err := os.MkdirAll(submod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(submod, "go.mod"), []byte("module example.com/sub\ngo 1.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Join(submod, "pkg")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(pkg)
		if !ok {
			t.Error("expected ok=true")
		}
		// .git is at gitroot — tier-1 pass finds it first.
		if got != gitroot {
			t.Errorf("root = %q, want gitroot %q (git beats go.mod)", got, gitroot)
		}
	})

	t.Run("nearest manifest wins among manifests", func(t *testing.T) {
		// outer/Makefile and outer/inner/go.mod — cwd is outer/inner/src.
		// Nearest to cwd is inner (has go.mod), so inner wins.
		outer := t.TempDir()
		if err := os.WriteFile(filepath.Join(outer, "Makefile"), []byte("all:\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		inner := filepath.Join(outer, "inner")
		if err := os.MkdirAll(inner, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inner, "go.mod"), []byte("module example.com/inner\ngo 1.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(inner, "src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(src)
		if !ok {
			t.Error("expected ok=true")
		}
		if got != inner {
			t.Errorf("root = %q, want inner %q (nearest manifest wins)", got, inner)
		}
	})

	t.Run("no markers falls back to cwd", func(t *testing.T) {
		dir := t.TempDir()
		got, ok := findProjectRoot(dir)
		if ok {
			t.Error("expected ok=false when no markers found")
		}
		if got != dir {
			t.Errorf("root = %q, want cwd %q", got, dir)
		}
	})

	t.Run("package.json marker", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(root)
		if !ok {
			t.Error("expected ok=true for package.json")
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
	})

	t.Run(".git above HOME is not selected", func(t *testing.T) {
		// Create a fake HOME and place .git ABOVE it.
		// The walk must stop at HOME and not pick up that .git.
		aboveHome := t.TempDir()
		if err := os.Mkdir(filepath.Join(aboveHome, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		// fakeHome is a child of aboveHome — HOME is below the .git.
		fakeHome2 := filepath.Join(aboveHome, "users", "alice")
		if err := os.MkdirAll(fakeHome2, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", fakeHome2)
		cwd := filepath.Join(fakeHome2, "projects", "myapp")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		got, ok := findProjectRoot(cwd)
		// Must NOT return aboveHome (the .git above HOME).
		if ok {
			t.Errorf("expected ok=false (walk stopped at HOME), got root=%q", got)
		}
		// Fallback should be cwd itself.
		if got != cwd {
			t.Errorf("fallback root = %q, want cwd %q", got, cwd)
		}
	})
}

func TestLoadAppConfigMarkerScope(t *testing.T) {
	// Tests the zero-config path: no .known.yaml, scope derived from marker root.
	tests := []struct {
		name       string
		setup      func(t *testing.T, root string) // creates markers in root
		cwdSubdir  string                          // relative path from root to chdir into
		wantPrefix string                          // expected ScopePrefix (root dir name)
		wantScope  string                          // expected DefaultScope
	}{
		{
			name: "git root, cwd at root",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			// Root dir name from t.TempDir() will be something like TestLoadAppConfigMarkerScope_git_root__cwd_at_root001
			// but we only check it's non-empty and equal to base(root).
		},
		{
			name: "go.mod root, cwd in subdir",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\ngo 1.21\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			cwdSubdir: "cmd/server",
		},
		{
			name: "no markers at all fallback",
			setup: func(t *testing.T, root string) {
				// Nothing — fallback to root itself.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeHome := resetGlobalState(t)
			_ = fakeHome

			root := t.TempDir()
			tt.setup(t, root)

			cwd := root
			if tt.cwdSubdir != "" {
				cwd = filepath.Join(root, tt.cwdSubdir)
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
			}
			t.Chdir(cwd)

			cfg, err := loadAppConfig(globalFlags{})
			if err != nil {
				t.Fatalf("loadAppConfig: %v", err)
			}

			// ScopeRoot must be set (to the marker root or cwd fallback).
			if cfg.ScopeRoot == "" {
				t.Error("ScopeRoot should not be empty (marker or fallback)")
			}

			// ScopePrefix must match the sanitized base dir name of the marker root.
			wantPrefix := sanitizeScopePrefix(filepath.Base(cfg.ScopeRoot))
			if cfg.ScopePrefix != wantPrefix {
				t.Errorf("ScopePrefix = %q, want %q (sanitized base of ScopeRoot %q)", cfg.ScopePrefix, wantPrefix, cfg.ScopeRoot)
			}

			// DefaultScope must not be empty.
			if cfg.DefaultScope == "" {
				t.Error("DefaultScope should not be empty")
			}
		})
	}
}

// isValidScopeSegmentForTest mirrors model.IsValidScopeSegment without importing model in test.
// Duplicated to keep test self-contained.
func isValidScopeSegmentForTest(s string) bool {
	if len(s) == 0 {
		return false
	}
	c := s[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	for _, r := range s[1:] {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func TestSanitizeScopePrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain valid", "myproject", "myproject"},
		{"hyphenated", "my-project", "my-project"},
		{"underscore", "my_project", "my_project"},
		{"dotted name", "my.app.v1", "my-app-v1"},
		{"digit-first", "2048game", "game"},
		{"digit-first complex", "123-foo", "foo"},
		{"leading dot hidden dir", ".hidden", "hidden"},
		{"all invalid chars", "...", ""},
		{"version tag", "v2.0.1", "v2-0-1"},
		{"mixed special", "project@v2", "project-v2"},
		{"trailing separator", "foo-", "foo"},
		{"empty", "", ""},
		{"only digits", "123", ""},
		{"single letter", "x", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeScopePrefix(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeScopePrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDSNOnlyYAMLPreservesMarkerScope(t *testing.T) {
	// BLOCKER 2 regression: a .known.yaml with only 'dsn:' must not collapse
	// scope to "root". When .known.yaml has no scope_prefix, the prefix is
	// derived from the yaml's own directory name (branch b in loadAppConfig),
	// NOT from a marker walk. The .git here is irrelevant to prefix derivation
	// but confirms that marker files don't interfere with yaml-sourced scope.
	fakeHome := resetGlobalState(t)

	// Use a named subdirectory so the dir name is a valid scope segment.
	root := filepath.Join(fakeHome, "myapp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// .git present but irrelevant: prefix comes from base(ScopeRoot), not a marker walk.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a DSN-only .known.yaml (no scope_prefix field).
	yamlContent := "dsn: postgres://test\n"
	if err := os.WriteFile(filepath.Join(root, projectConfigFile), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	cfg, err := loadAppConfig(globalFlags{})
	if err != nil {
		t.Fatalf("loadAppConfig: %v", err)
	}

	// DSN must come from yaml.
	if cfg.DSN != "postgres://test" {
		t.Errorf("DSN = %q, want postgres://test", cfg.DSN)
	}

	// ScopePrefix must be "myapp" — derived from base(ScopeRoot) because
	// .known.yaml exists but has no scope_prefix (branch b, not a marker walk).
	if cfg.ScopePrefix != "myapp" {
		t.Errorf("ScopePrefix = %q, want %q", cfg.ScopePrefix, "myapp")
	}

	// DefaultScope must reflect the prefix (cwd == root so no subdir component).
	if cfg.DefaultScope != "myapp" {
		t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, "myapp")
	}
}
