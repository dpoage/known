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
		if cfg.DefaultTTL["conversation"] != "168h" {
			t.Errorf("DefaultTTL[conversation] = %q, want %q", cfg.DefaultTTL["conversation"], "168h")
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
