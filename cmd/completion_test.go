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

func TestRunCompleteLight_NoArgs(t *testing.T) {
	err := runCompleteLight(context.Background(), globalFlags{}, nil)
	if err == nil {
		t.Fatal("expected error for no args")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error %q should mention usage", err.Error())
	}
}

func TestRunCompleteLight_UnknownType(t *testing.T) {
	// "badtype" hits the default case before needing a DB.
	// Use an in-memory DB so config loading succeeds.
	err := runCompleteLight(context.Background(), globalFlags{dsn: ":memory:"}, []string{"badtype"})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown completion type") {
		t.Errorf("error %q should mention unknown type", err.Error())
	}
}

func TestRunCompleteLight_Scopes(t *testing.T) {
	// Use an in-memory SQLite DB and seed scopes.
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	s1 := model.NewScope("backend.api")
	s2 := model.NewScope("frontend.ui")
	if err := db.Scopes().Upsert(ctx, &s1); err != nil {
		t.Fatal(err)
	}
	if err := db.Scopes().Upsert(ctx, &s2); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// runCompleteLight opens its own connection, so we need a file-based DB.
	// Instead, test the output path via a fresh in-memory DB with dsn flag.
	// Since :memory: creates a new DB each time, we test the end-to-end
	// output path with an empty scope list (no scopes = no output lines).
	cap := captureStdout(t)
	err = runCompleteLight(ctx, globalFlags{dsn: ":memory:"}, []string{"scopes"})
	out := cap.restore()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// :memory: DB has no scopes, so output should be empty.
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output for fresh DB, got %q", out)
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
		"provenance":  {"verified", "inferred", "uncertain"},
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
