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
			wantErr: "flag provided but not defined",
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
