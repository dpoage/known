package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/spf13/viper"
)

// resetGlobalState clears Viper, sets HOME to a temp dir, and clears KNOWN_DSN.
// Returns the fake home directory path.
func resetGlobalState(t *testing.T) string {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("KNOWN_DSN", "")
	return fakeHome
}

func TestExpandHome(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tilde prefix",
			input: "~/foo/bar",
			want:  filepath.Join(fakeHome, "foo/bar"),
		},
		{
			name:  "absolute path",
			input: "/absolute/path",
			want:  "/absolute/path",
		},
		{
			name:  "relative no tilde",
			input: "relative/path",
			want:  "relative/path",
		},
		{
			name:  "tilde without slash",
			input: "~nope",
			want:  "~nope",
		},
		{
			name:  "just tilde slash",
			input: "~/",
			want:  fakeHome,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.input)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQualifyScope(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		scope  string
		want   string
	}{
		{name: "empty scope", prefix: "myapp", scope: "", want: ""},
		{name: "no prefix passthrough", prefix: "", scope: "cmd", want: "cmd"},
		{name: "bare segment prefixed", prefix: "myapp", scope: "cmd", want: "myapp.cmd"},
		{name: "root maps to prefix", prefix: "myapp", scope: "root", want: "myapp"},
		{name: "already qualified exact", prefix: "myapp", scope: "myapp", want: "myapp"},
		{name: "already qualified dotted", prefix: "myapp", scope: "myapp.cmd", want: "myapp.cmd"},
		{name: "no false match on prefix substring", prefix: "my", scope: "myself", want: "my.myself"},
		{name: "literal slash stripped", prefix: "myapp", scope: "/services", want: "services"},
		{name: "literal root via slash", prefix: "myapp", scope: "/root", want: "root"},
		{name: "literal slash no prefix", prefix: "", scope: "/services", want: "services"},
		{name: "no prefix root passthrough", prefix: "", scope: "root", want: "root"},
		{name: "deep scope prefixed", prefix: "myapp", scope: "cmd.api", want: "myapp.cmd.api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AppConfig{ScopePrefix: tt.prefix}
			got := cfg.QualifyScope(tt.scope)
			if got != tt.want {
				t.Errorf("QualifyScope(%q) with prefix=%q = %q, want %q", tt.scope, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestLoadAppConfig(t *testing.T) {
	type testCase struct {
		name        string
		gf          globalFlags
		env         map[string]string
		projectYAML string // written as .known.yaml in cwd
		globalYAML  string // written as $HOME/.known/config.yaml
		cwdSubdir   string // subdir under project root to chdir into
		wantErr     string // error substring (empty = expect success)
		check       func(t *testing.T, cfg *AppConfig)
	}

	tests := []testCase{
		// --- DSN resolution priority ---
		{
			name: "dsn from flag",
			gf:   globalFlags{dsn: "postgres://flag"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DSN != "postgres://flag" {
					t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://flag")
				}
			},
		},
		{
			name: "dsn from env",
			env:  map[string]string{"KNOWN_DSN": "postgres://env"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DSN != "postgres://env" {
					t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://env")
				}
			},
		},
		{
			name:        "dsn from project config",
			projectYAML: "dsn: postgres://project\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DSN != "postgres://project" {
					t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://project")
				}
			},
		},
		{
			name:       "dsn from global config",
			globalYAML: "dsn: postgres://global\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DSN != "postgres://global" {
					t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://global")
				}
			},
		},
		{
			name:        "flag beats env beats project beats global",
			gf:          globalFlags{dsn: "postgres://flag"},
			env:         map[string]string{"KNOWN_DSN": "postgres://env"},
			projectYAML: "dsn: postgres://project\n",
			globalYAML:  "dsn: postgres://global\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DSN != "postgres://flag" {
					t.Errorf("DSN = %q, want %q", cfg.DSN, "postgres://flag")
				}
			},
		},
		{
			name: "no dsn anywhere defaults to sqlite",
			check: func(t *testing.T, cfg *AppConfig) {
				home, _ := os.UserHomeDir()
				want := filepath.Join(home, ".known", "known.db")
				if cfg.DSN != want {
					t.Errorf("DSN = %q, want %q", cfg.DSN, want)
				}
			},
		},

		// --- ScopeRoot + DefaultScope ---
		{
			name:        "scope root from project dir",
			projectYAML: "dsn: postgres://test\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.ScopeRoot == "" {
					t.Error("ScopeRoot should be set when .known.yaml found")
				}
				// cwd == project root, so DefaultScope should be "root"
				if cfg.DefaultScope != model.RootScope {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, model.RootScope)
				}
			},
		},
		{
			name:       "scope root from global config",
			globalYAML: "dsn: postgres://test\nscope_root: /tmp/test-scope\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.ScopeRoot != "/tmp/test-scope" {
					t.Errorf("ScopeRoot = %q, want %q", cfg.ScopeRoot, "/tmp/test-scope")
				}
			},
		},
		{
			name: "scope root tilde expansion via global config",
			globalYAML: func() string {
				return "dsn: postgres://test\nscope_root: ~/projects\n"
			}(),
			check: func(t *testing.T, cfg *AppConfig) {
				// HOME is the fake home; expandHome should have expanded ~/projects
				if filepath.Base(cfg.ScopeRoot) != "projects" {
					t.Errorf("ScopeRoot = %q, want suffix 'projects'", cfg.ScopeRoot)
				}
				// Should not start with ~
				if cfg.ScopeRoot[0] == '~' {
					t.Errorf("ScopeRoot = %q, tilde not expanded", cfg.ScopeRoot)
				}
			},
		},
		{
			name:        "default scope derived from cwd subdir",
			projectYAML: "dsn: postgres://test\n",
			cwdSubdir:   "services/api",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultScope != "services.api" {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, "services.api")
				}
			},
		},
		{
			name: "default scope is root when no scope root",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultScope != model.RootScope {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, model.RootScope)
				}
			},
		},

		// --- ScopePrefix ---
		{
			name:        "scope prefix at project root",
			projectYAML: "dsn: postgres://test\nscope_prefix: myapp\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultScope != "myapp" {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, "myapp")
				}
			},
		},
		{
			name:        "scope prefix with cwd subdir",
			projectYAML: "dsn: postgres://test\nscope_prefix: myapp\n",
			cwdSubdir:   "services/api",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultScope != "myapp.services.api" {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, "myapp.services.api")
				}
			},
		},
		{
			name:        "no scope prefix preserves root behavior",
			projectYAML: "dsn: postgres://test\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultScope != model.RootScope {
					t.Errorf("DefaultScope = %q, want %q", cfg.DefaultScope, model.RootScope)
				}
			},
		},
		{
			name:        "scope prefix field populated from project yaml",
			projectYAML: "dsn: postgres://test\nscope_prefix: myapp\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.ScopePrefix != "myapp" {
					t.Errorf("ScopePrefix = %q, want %q", cfg.ScopePrefix, "myapp")
				}
			},
		},

		// --- MaxContentLength cascade ---
		{
			name:        "max content length from project config",
			projectYAML: "dsn: postgres://test\nmax_content_length: 8192\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.MaxContentLength != 8192 {
					t.Errorf("MaxContentLength = %d, want 8192", cfg.MaxContentLength)
				}
			},
		},
		{
			name:       "max content length from global config",
			globalYAML: "dsn: postgres://test\nmax_content_length: 2048\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.MaxContentLength != 2048 {
					t.Errorf("MaxContentLength = %d, want 2048", cfg.MaxContentLength)
				}
			},
		},
		{
			name: "max content length hardcoded default",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.MaxContentLength != model.MaxContentLength {
					t.Errorf("MaxContentLength = %d, want %d", cfg.MaxContentLength, model.MaxContentLength)
				}
			},
		},

		// --- SearchThreshold cascade ---
		{
			name:        "search threshold from project config",
			projectYAML: "dsn: postgres://test\nsearch_threshold: 0.5\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.SearchThreshold != 0.5 {
					t.Errorf("SearchThreshold = %f, want 0.5", cfg.SearchThreshold)
				}
			},
		},
		{
			name:       "search threshold from global config",
			globalYAML: "dsn: postgres://test\nsearch_threshold: 0.7\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.SearchThreshold != 0.7 {
					t.Errorf("SearchThreshold = %f, want 0.7", cfg.SearchThreshold)
				}
			},
		},
		{
			name: "search threshold hardcoded default",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.SearchThreshold != 0.4 {
					t.Errorf("SearchThreshold = %f, want 0.4", cfg.SearchThreshold)
				}
			},
		},

		// --- RecallLimit cascade ---
		{
			name:        "recall limit from project config",
			projectYAML: "dsn: postgres://test\nrecall_limit: 10\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallLimit != 10 {
					t.Errorf("RecallLimit = %d, want 10", cfg.RecallLimit)
				}
			},
		},
		{
			name:       "recall limit from global config",
			globalYAML: "dsn: postgres://test\nrecall_limit: 15\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallLimit != 15 {
					t.Errorf("RecallLimit = %d, want 15", cfg.RecallLimit)
				}
			},
		},
		{
			name: "recall limit hardcoded default",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallLimit != 5 {
					t.Errorf("RecallLimit = %d, want 5", cfg.RecallLimit)
				}
			},
		},

		// --- RecallExpandDepth cascade ---
		{
			name:        "recall expand depth from project config",
			projectYAML: "dsn: postgres://test\nrecall_expand_depth: 2\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallExpandDepth != 2 {
					t.Errorf("RecallExpandDepth = %d, want 2", cfg.RecallExpandDepth)
				}
			},
		},
		{
			name: "recall expand depth hardcoded default",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallExpandDepth != 0 {
					t.Errorf("RecallExpandDepth = %d, want 0", cfg.RecallExpandDepth)
				}
			},
		},

		// --- RecallRecency cascade ---
		{
			name:        "recall recency from project config",
			projectYAML: "dsn: postgres://test\nrecall_recency: 0.3\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallRecency != 0.3 {
					t.Errorf("RecallRecency = %f, want 0.3", cfg.RecallRecency)
				}
			},
		},
		{
			name: "recall recency hardcoded default",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.RecallRecency != 0.1 {
					t.Errorf("RecallRecency = %f, want 0.1", cfg.RecallRecency)
				}
			},
		},

		// --- DefaultTTL ---
		{
			name: "default ttl hardcoded defaults",
			gf:   globalFlags{dsn: "postgres://test"},
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultTTL[model.SourceConversation] != 7*24*time.Hour {
					t.Errorf("conversation TTL = %v, want 7d", cfg.DefaultTTL[model.SourceConversation])
				}
				if cfg.DefaultTTL[model.SourceManual] != 90*24*time.Hour {
					t.Errorf("manual TTL = %v, want 90d", cfg.DefaultTTL[model.SourceManual])
				}
			},
		},
		{
			name:       "default ttl global config overrides",
			globalYAML: "dsn: postgres://test\ndefault_ttl:\n  conversation: 48h\n  manual: 720h\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultTTL[model.SourceConversation] != 48*time.Hour {
					t.Errorf("conversation TTL = %v, want 48h", cfg.DefaultTTL[model.SourceConversation])
				}
				if cfg.DefaultTTL[model.SourceManual] != 720*time.Hour {
					t.Errorf("manual TTL = %v, want 720h", cfg.DefaultTTL[model.SourceManual])
				}
			},
		},
		{
			name:        "project ttl beats global ttl",
			globalYAML:  "dsn: postgres://test\ndefault_ttl:\n  conversation: 48h\n",
			projectYAML: "dsn: postgres://test\ndefault_ttl:\n  conversation: 24h\n",
			check: func(t *testing.T, cfg *AppConfig) {
				if cfg.DefaultTTL[model.SourceConversation] != 24*time.Hour {
					t.Errorf("conversation TTL = %v, want 24h", cfg.DefaultTTL[model.SourceConversation])
				}
			},
		},

		// --- Error paths ---
		{
			name:        "invalid ttl in project config",
			projectYAML: "dsn: postgres://test\ndefault_ttl:\n  conversation: notaduration\n",
			wantErr:     "invalid project default_ttl.conversation",
		},
		{
			name:        "malformed project yaml",
			projectYAML: ":\n  invalid: yaml: [broken",
			gf:          globalFlags{dsn: "postgres://test"},
			wantErr:     "read project config",
		},

		// --- Flag passthrough ---
		{
			name: "json and quiet flags passed through",
			gf:   globalFlags{dsn: "postgres://test", json: true, quiet: true},
			check: func(t *testing.T, cfg *AppConfig) {
				if !cfg.JSON {
					t.Error("JSON should be true")
				}
				if !cfg.Quiet {
					t.Error("Quiet should be true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeHome := resetGlobalState(t)

			// Set up project directory as tmpdir (may also be cwd).
			projectDir := t.TempDir()

			// Write project-local .known.yaml if specified.
			if tt.projectYAML != "" {
				err := os.WriteFile(filepath.Join(projectDir, projectConfigFile), []byte(tt.projectYAML), 0o644)
				if err != nil {
					t.Fatalf("write project config: %v", err)
				}
			}

			// Write global config if specified.
			if tt.globalYAML != "" {
				globalDir := filepath.Join(fakeHome, ".known")
				if err := os.MkdirAll(globalDir, 0o755); err != nil {
					t.Fatalf("mkdir global config dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), []byte(tt.globalYAML), 0o644); err != nil {
					t.Fatalf("write global config: %v", err)
				}
			}

			// Set environment variables.
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			// Determine cwd: optionally a subdir under the project root.
			cwd := projectDir
			if tt.cwdSubdir != "" {
				cwd = filepath.Join(projectDir, tt.cwdSubdir)
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatalf("mkdir cwdSubdir: %v", err)
				}
			}
			t.Chdir(cwd)

			cfg, err := loadAppConfig(tt.gf)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
