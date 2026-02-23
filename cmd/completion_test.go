package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
)

// allSubcommands returns the names of all registered commands.
func allSubcommands() []string {
	names := make([]string, len(commands))
	for i, cmd := range commands {
		names[i] = cmd.name
	}
	return names
}

// stubScopeRepo implements storage.ScopeRepo for tests.
type stubScopeRepo struct {
	scopes []model.Scope
}

func (s *stubScopeRepo) Upsert(context.Context, *model.Scope) error                  { return nil }
func (s *stubScopeRepo) Get(context.Context, string) (*model.Scope, error)            { return nil, nil }
func (s *stubScopeRepo) Delete(context.Context, string) error                         { return nil }
func (s *stubScopeRepo) List(context.Context) ([]model.Scope, error)                  { return s.scopes, nil }
func (s *stubScopeRepo) ListChildren(context.Context, string) ([]model.Scope, error)  { return nil, nil }
func (s *stubScopeRepo) ListDescendants(context.Context, string) ([]model.Scope, error) {
	return nil, nil
}

func TestRunCompletion_NoArgs(t *testing.T) {
	err := runCompletion(nil)
	if err == nil {
		t.Fatal("expected error for no args")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error %q should mention usage", err.Error())
	}
}

func TestRunCompletion_UnknownShell(t *testing.T) {
	err := runCompletion([]string{"powershell"})
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
	if !strings.Contains(err.Error(), "powershell") {
		t.Errorf("error %q should mention the shell name", err.Error())
	}
	if !strings.Contains(err.Error(), "bash, fish, or zsh") {
		t.Errorf("error %q should list supported shells", err.Error())
	}
}

func TestRunComplete_NoArgs(t *testing.T) {
	app := &App{Scopes: &stubScopeRepo{}}
	err := runComplete(context.Background(), app, nil)
	if err == nil {
		t.Fatal("expected error for no args")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error %q should mention usage", err.Error())
	}
}

func TestRunComplete_UnknownType(t *testing.T) {
	app := &App{Scopes: &stubScopeRepo{}}
	err := runComplete(context.Background(), app, []string{"badtype"})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown completion type") {
		t.Errorf("error %q should mention unknown type", err.Error())
	}
}

func TestRunComplete_Scopes(t *testing.T) {
	repo := &stubScopeRepo{
		scopes: []model.Scope{
			{Path: "backend.api"},
			{Path: "frontend.ui"},
		},
	}
	app := &App{
		Scopes:  repo,
		Printer: NewPrinter(&bytes.Buffer{}, false, true),
	}

	// Capture stdout.
	old := captureStdout(t)
	err := runComplete(context.Background(), app, []string{"scopes"})
	out := old.restore()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "backend.api" {
		t.Errorf("line 0 = %q, want %q", lines[0], "backend.api")
	}
	if lines[1] != "frontend.ui" {
		t.Errorf("line 1 = %q, want %q", lines[1], "frontend.ui")
	}
}

// stdoutCapture helps capture os.Stdout in tests.
type stdoutCapture struct {
	t    *testing.T
	orig *os.File
	r    *os.File
	w    *os.File
}

func captureStdout(t *testing.T) *stdoutCapture {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	return &stdoutCapture{t: t, orig: orig, r: r, w: w}
}

func (c *stdoutCapture) restore() string {
	c.t.Helper()
	c.w.Close()
	os.Stdout = c.orig
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(c.r)
	return buf.String()
}

func TestDynamicCompletionInBash(t *testing.T) {
	script := generateBash()
	if !strings.Contains(script, "known __complete scopes") {
		t.Error("bash script missing dynamic scope completion")
	}
}

func TestDynamicCompletionInFish(t *testing.T) {
	script := generateFish()
	if !strings.Contains(script, "known __complete scopes") {
		t.Error("fish script missing dynamic scope completion")
	}
}

func TestDynamicCompletionInZsh(t *testing.T) {
	script := generateZsh()
	if !strings.Contains(script, "known __complete scopes") {
		t.Error("zsh script missing dynamic scope completion")
	}
	if !strings.Contains(script, "__known_complete_scopes") {
		t.Error("zsh script missing helper function __known_complete_scopes")
	}
}

func TestBashContainsAllSubcommands(t *testing.T) {
	script := generateBash()
	for _, name := range allSubcommands() {
		if !strings.Contains(script, name) {
			t.Errorf("bash script missing subcommand %q", name)
		}
	}
}

func TestFishContainsAllSubcommands(t *testing.T) {
	script := generateFish()
	for _, name := range allSubcommands() {
		if !strings.Contains(script, name) {
			t.Errorf("fish script missing subcommand %q", name)
		}
	}
}

func TestZshContainsAllSubcommands(t *testing.T) {
	script := generateZsh()
	for _, name := range allSubcommands() {
		if !strings.Contains(script, name) {
			t.Errorf("zsh script missing subcommand %q", name)
		}
	}
}

func TestEnumFlagsInOutput(t *testing.T) {
	enums := map[string][]string{
		"source-type": {"file", "url", "conversation", "manual"},
		"confidence":  {"verified", "inferred", "uncertain"},
		"direction":   {"out", "outgoing", "in", "incoming", "both"},
		"format":      {"json", "jsonl"},
		"type":        {"depends-on", "contradicts", "supersedes", "elaborates", "related-to"},
	}

	generators := map[string]func() string{
		"bash": generateBash,
		"fish": generateFish,
		"zsh":  generateZsh,
	}

	for shell, gen := range generators {
		script := gen()
		for flag, values := range enums {
			for _, v := range values {
				if !strings.Contains(script, v) {
					t.Errorf("%s script missing enum value %q for flag --%s", shell, v, flag)
				}
			}
		}
	}
}

func TestBashScopeSubcommands(t *testing.T) {
	script := generateBash()
	for _, sub := range []string{"list", "create", "tree"} {
		if !strings.Contains(script, sub) {
			t.Errorf("bash script missing scope subcommand %q", sub)
		}
	}
}

func TestBashSyntax(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script := generateBash()
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\noutput: %s", err, out)
	}
}

func TestFishSyntax(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not available")
	}
	script := generateFish()
	cmd := exec.Command("fish", "--no-execute")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish --no-execute failed: %v\noutput: %s", err, out)
	}
}

func TestZshSyntax(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	script := generateZsh()
	cmd := exec.Command("zsh", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh -n failed: %v\noutput: %s", err, out)
	}
}
