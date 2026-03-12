package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// stubScopeRepo implements storage.ScopeRepo with an in-memory scope list.
type stubScopeRepo struct {
	scopes []model.Scope
}

func (s *stubScopeRepo) Upsert(context.Context, *model.Scope) error      { return nil }
func (s *stubScopeRepo) EnsureHierarchy(context.Context, string) error    { return nil }
func (s *stubScopeRepo) Delete(context.Context, string) error             { return nil }
func (s *stubScopeRepo) ListChildren(context.Context, string) ([]model.Scope, error) {
	return nil, nil
}
func (s *stubScopeRepo) ListDescendants(context.Context, string) ([]model.Scope, error) {
	return nil, nil
}
func (s *stubScopeRepo) DeleteEmpty(context.Context) (int64, error) { return 0, nil }

func (s *stubScopeRepo) Get(_ context.Context, path string) (*model.Scope, error) {
	for i := range s.scopes {
		if s.scopes[i].Path == path {
			return &s.scopes[i], nil
		}
	}
	return nil, storage.ErrNotFound
}

func (s *stubScopeRepo) List(_ context.Context) ([]model.Scope, error) {
	return s.scopes, nil
}

func TestResolveScope(t *testing.T) {
	scopes := []model.Scope{
		{Path: "the_cloud"},
		{Path: "the_cloud.api"},
		{Path: "the_cloud.db"},
		{Path: "copilot"},
		{Path: "copilot.auth"},
		{Path: "reactions"},
		{Path: "reactions.auth"},
	}

	tests := []struct {
		name       string
		prefix     string
		input      string
		scopes     []model.Scope
		want       string
		wantStderr string // substring expected in stderr (empty = no warning)
	}{
		{
			name:   "empty input",
			prefix: "the_cloud",
			input:  "",
			scopes: scopes,
			want:   "",
		},
		{
			name:   "literal slash stripped",
			prefix: "the_cloud",
			input:  "/copilot",
			scopes: scopes,
			want:   "copilot",
		},
		{
			name:   "no prefix passthrough",
			prefix: "",
			input:  "copilot",
			scopes: scopes,
			want:   "copilot",
		},
		{
			name:   "qualified form exists",
			prefix: "the_cloud",
			input:  "api",
			scopes: scopes,
			want:   "the_cloud.api",
		},
		{
			name:   "bare form exists when qualified does not",
			prefix: "the_cloud",
			input:  "copilot",
			scopes: scopes,
			want:   "copilot",
		},
		{
			name:   "suffix match single hit",
			prefix: "myapp",
			input:  "reactions",
			scopes: scopes,
			want:   "reactions",
		},
		{
			name:       "suffix match ambiguous warns",
			prefix:     "myapp",
			input:      "auth",
			scopes:     scopes,
			want:       "myapp.auth",
			wantStderr: "ambiguous scope",
		},
		{
			name:   "no scope exists falls back to qualified",
			prefix: "the_cloud",
			input:  "nonexistent",
			scopes: scopes,
			want:   "the_cloud.nonexistent",
		},
		{
			name:   "root maps to prefix then checks DB",
			prefix: "the_cloud",
			input:  "root",
			scopes: scopes,
			want:   "the_cloud", // qualified form = prefix, which exists
		},
		{
			name:   "already qualified not double-prefixed",
			prefix: "the_cloud",
			input:  "the_cloud.api",
			scopes: scopes,
			want:   "the_cloud.api",
		},
		{
			name:   "dotted input qualified exists",
			prefix: "copilot",
			input:  "auth",
			scopes: scopes,
			want:   "copilot.auth",
		},
		{
			name:   "empty scope list falls back to qualified",
			prefix: "the_cloud",
			input:  "whatever",
			scopes: nil,
			want:   "the_cloud.whatever",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			app := &App{
				Scopes: &stubScopeRepo{scopes: tt.scopes},
				Stderr: &stderr,
				Config: &AppConfig{ScopePrefix: tt.prefix},
			}

			got := app.ResolveScope(context.Background(), tt.input)

			if got != tt.want {
				t.Errorf("ResolveScope(%q) with prefix=%q = %q, want %q",
					tt.input, tt.prefix, got, tt.want)
			}

			stderrStr := stderr.String()
			if tt.wantStderr != "" {
				if !strings.Contains(stderrStr, tt.wantStderr) {
					t.Errorf("expected stderr containing %q, got %q", tt.wantStderr, stderrStr)
				}
			} else if stderrStr != "" {
				t.Errorf("unexpected stderr output: %q", stderrStr)
			}
		})
	}
}
