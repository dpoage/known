package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRunInit(t *testing.T) {
	type testCase struct {
		name        string
		args        []string
		envDSN      string
		preExisting string // pre-existing .known.yaml content
		wantErr     string // error substring
		wantDSN     string // expected DSN in written file
	}

	// Compute the expected default SQLite DSN.
	home, _ := os.UserHomeDir()
	defaultDSN := filepath.Join(home, ".known", "known.db")

	tests := []testCase{
		{
			name:    "dsn from flag",
			args:    []string{"--dsn", "postgres://flag"},
			wantDSN: "postgres://flag",
		},
		{
			name:    "dsn from env",
			args:    []string{},
			envDSN:  "postgres://env",
			wantDSN: "postgres://env",
		},
		{
			name:    "flag beats env",
			args:    []string{"--dsn", "postgres://flag"},
			envDSN:  "postgres://env",
			wantDSN: "postgres://flag",
		},
		{
			name:    "no dsn defaults to sqlite",
			args:    []string{},
			wantDSN: defaultDSN,
		},
		{
			name:        "exists without force",
			args:        []string{"--dsn", "postgres://x"},
			preExisting: "dsn: old\n",
			wantErr:     "already exists",
		},
		{
			name:        "force overwrites",
			args:        []string{"--dsn", "pg://new", "--force"},
			preExisting: "dsn: old\n",
			wantDSN:     "pg://new",
		},
		{
			name:    "invalid flag errors",
			args:    []string{"--unknown"},
			wantErr: "unknown flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			// Clear KNOWN_DSN by default; set if test specifies.
			t.Setenv("KNOWN_DSN", tt.envDSN)

			// Write pre-existing .known.yaml if specified.
			if tt.preExisting != "" {
				if err := os.WriteFile(filepath.Join(tmpDir, projectConfigFile), []byte(tt.preExisting), 0o644); err != nil {
					t.Fatalf("write pre-existing config: %v", err)
				}
			}

			err := runInit(nil, tt.args)

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

			// Verify the written file.
			data, err := os.ReadFile(filepath.Join(tmpDir, projectConfigFile))
			if err != nil {
				t.Fatalf("read written config: %v", err)
			}

			var cfg projectConfig
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("unmarshal written config: %v", err)
			}

			if cfg.DSN != tt.wantDSN {
				t.Errorf("DSN = %q, want %q", cfg.DSN, tt.wantDSN)
			}
		})
	}
}

func TestRunInitScaffold(t *testing.T) {
	skillFiles := []string{
		".claude/CLAUDE.md",
		".claude/settings.json",
		".claude/skills/remember/SKILL.md",
		".claude/skills/recall/SKILL.md",
		".claude/skills/forget/SKILL.md",
		".claude/skills/known-search/SKILL.md",
	}

	t.Run("creates skills by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)
		t.Setenv("KNOWN_DSN", "")

		if err := runInit(nil, []string{"--dsn", "sqlite:///test.db"}); err != nil {
			t.Fatalf("runInit: %v", err)
		}

		for _, f := range skillFiles {
			path := filepath.Join(tmpDir, f)
			info, err := os.Stat(path)
			if err != nil {
				t.Errorf("expected %s to exist: %v", f, err)
				continue
			}
			if info.Size() == 0 {
				t.Errorf("expected %s to be non-empty", f)
			}
		}
	})

	t.Run("no-scaffold skips skills", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)
		t.Setenv("KNOWN_DSN", "")

		if err := runInit(nil, []string{"--dsn", "sqlite:///test.db", "--no-scaffold"}); err != nil {
			t.Fatalf("runInit: %v", err)
		}

		claudeDir := filepath.Join(tmpDir, ".claude")
		if _, err := os.Stat(claudeDir); !os.IsNotExist(err) {
			t.Errorf("expected .claude/ to not exist with --no-scaffold, but it does")
		}
	})

	t.Run("force does not clobber existing skills", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)
		t.Setenv("KNOWN_DSN", "")

		// First init.
		if err := runInit(nil, []string{"--dsn", "sqlite:///test.db"}); err != nil {
			t.Fatalf("first runInit: %v", err)
		}

		// Modify a skill file to detect overwrites.
		marker := "# CUSTOM CONTENT"
		skillPath := filepath.Join(tmpDir, ".claude/skills/remember/SKILL.md")
		if err := os.WriteFile(skillPath, []byte(marker), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}

		// Re-init with --force.
		if err := runInit(nil, []string{"--dsn", "sqlite:///test.db", "--force"}); err != nil {
			t.Fatalf("second runInit: %v", err)
		}

		// Verify the marker survived.
		data, err := os.ReadFile(skillPath)
		if err != nil {
			t.Fatalf("read skill: %v", err)
		}
		if string(data) != marker {
			t.Errorf("skill file was overwritten: got %q, want %q", string(data), marker)
		}
	})
}
