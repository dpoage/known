//go:build integration

// Package cli_test exercises the known CLI binary end-to-end.
// Each test gets an isolated temp directory with its own .known.yaml and
// SQLite database, so tests do not interfere with each other or the
// user's real data.
package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// knownBin is the path to the compiled known binary, built once in TestMain.
var knownBin string

// sharedHome is a fake HOME directory shared across all tests so the hugot
// embedder only downloads the ONNX model once. Database isolation is achieved
// via per-test KNOWN_DSN, not HOME.
var sharedHome string

func TestMain(m *testing.M) {
	// Build the known binary into a temp directory.
	tmpDir, err := os.MkdirTemp("", "known-cli-test-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for binary: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Create a shared fake HOME for model caching.
	sharedHome, err = os.MkdirTemp("", "known-cli-test-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create shared home dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(sharedHome)

	knownBin = filepath.Join(tmpDir, "known")
	cmd := exec.Command("go", "build", "-o", knownBin, "./cmd/known/")
	cmd.Dir = findModuleRoot()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build known binary: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// findModuleRoot walks up from the current working directory to find the
// directory containing go.mod.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic("cannot get working directory: " + err.Error())
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("cannot find go.mod in any parent directory")
		}
		dir = parent
	}
}

// runKnown executes the known binary in the given working directory with the
// provided arguments. It returns stdout, stderr, and the exit code.
func runKnown(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(knownBin, args...)
	cmd.Dir = dir

	dsnPath := filepath.Join(dir, "test.db")

	// Use sharedHome so the hugot ONNX model is downloaded once and cached.
	// Database isolation comes from per-test KNOWN_DSN, not HOME.
	cmd.Env = []string{
		"HOME=" + sharedHome,
		"KNOWN_DSN=" + dsnPath,
		"KNOWN_EMBEDDER=hugot",
		// Minimal PATH so the binary can find shared libraries if needed.
		"PATH=" + os.Getenv("PATH"),
		// Preserve TMPDIR / temp dir vars for the OS.
		"TMPDIR=" + os.TempDir(),
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run known: %v", err)
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// initKnown runs "known init" in the given directory and asserts success.
func initKnown(t *testing.T, dir string) {
	t.Helper()
	_, stderr, code := runKnown(t, dir, "init")
	if code != 0 {
		t.Fatalf("known init failed (exit %d): %s", code, stderr)
	}
}

// addEntry runs "known add" with the given content and optional extra args,
// asserts success, and returns the combined stdout.
func addEntry(t *testing.T, dir string, content string, extraArgs ...string) string {
	t.Helper()
	// Flags must come before the positional content argument because Go's
	// flag package stops parsing at the first non-flag arg.
	args := []string{"add", "--source-type", "manual", "--provenance", "verified"}
	args = append(args, extraArgs...)
	args = append(args, content)
	stdout, stderr, code := runKnown(t, dir, args...)
	if code != 0 {
		t.Fatalf("known add failed (exit %d): %s", code, stderr)
	}
	return stdout
}

// extractID extracts the first ULID-like ID from the output of "known add".
// The output format includes a line like "ID:         01J..."
func extractID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID:") {
			id := strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
			if id != "" {
				return id
			}
		}
	}
	t.Fatalf("could not find ID in output:\n%s", output)
	return ""
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCLI_InitAndScaffold(t *testing.T) {
	dir := t.TempDir()

	_, stderr, code := runKnown(t, dir, "init")
	if code != 0 {
		t.Fatalf("known init failed (exit %d): %s", code, stderr)
	}

	// .known.yaml must exist.
	if _, err := os.Stat(filepath.Join(dir, ".known.yaml")); err != nil {
		t.Fatalf(".known.yaml not created: %v", err)
	}

	// Scaffold should have created Claude Code skill files.
	skillPath := filepath.Join(dir, ".claude", "skills", "remember", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf(".claude/skills/remember/SKILL.md not created: %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf(".claude/settings.json not created: %v", err)
	}
}

func TestCLI_AddAndRecall(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	// Add an entry.
	stdout := addEntry(t, dir, "Go interfaces enable loose coupling")

	// Verify the output contains an ID.
	id := extractID(t, stdout)
	if id == "" {
		t.Fatal("add did not produce an ID")
	}

	// Recall should find the entry by semantic search.
	stdout, stderr, code := runKnown(t, dir, "recall", "interfaces")
	if code != 0 {
		t.Fatalf("known recall failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, "loose coupling") {
		t.Fatalf("recall output should contain 'loose coupling', got:\n%s", stdout)
	}
}

func TestCLI_AddAndSearch(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	addEntry(t, dir, "Go interfaces enable loose coupling")

	stdout, stderr, code := runKnown(t, dir, "search", "interfaces", "--limit", "5")
	if code != 0 {
		t.Fatalf("known search failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, "loose coupling") {
		t.Fatalf("search output should contain 'loose coupling', got:\n%s", stdout)
	}
}

func TestCLI_SearchJSON(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	addEntry(t, dir, "Go interfaces enable loose coupling")

	stdout, stderr, code := runKnown(t, dir, "--json", "search", "interfaces")
	if code != 0 {
		t.Fatalf("known --json search failed (exit %d): %s", code, stderr)
	}

	// stdout must be valid JSON. It should be an array of results.
	stdout = strings.TrimSpace(stdout)
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("search --json output is not valid JSON:\n%s", stdout)
	}
}

func TestCLI_DeleteFlow(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	stdout := addEntry(t, dir, "Temporary knowledge to delete")
	id := extractID(t, stdout)

	_, stderr, code := runKnown(t, dir, "delete", id, "--force")
	if code != 0 {
		t.Fatalf("known delete failed (exit %d): %s", code, stderr)
	}
}

func TestCLI_Stats(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	addEntry(t, dir, "First entry for stats")
	addEntry(t, dir, "Second entry for stats")

	stdout, stderr, code := runKnown(t, dir, "stats")
	if code != 0 {
		t.Fatalf("known stats failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, "Entries: 2") {
		t.Fatalf("stats output should contain 'Entries: 2', got:\n%s", stdout)
	}
}

func TestCLI_ScopeTree(t *testing.T) {
	dir := t.TempDir()
	initKnown(t, dir)

	addEntry(t, dir, "Backend knowledge", "--scope", "backend")
	addEntry(t, dir, "Frontend knowledge", "--scope", "frontend")

	stdout, stderr, code := runKnown(t, dir, "scope", "tree")
	if code != 0 {
		t.Fatalf("known scope tree failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, "/backend") {
		t.Fatalf("scope tree output should contain '/backend', got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "/frontend") {
		t.Fatalf("scope tree output should contain '/frontend', got:\n%s", stdout)
	}
}

func TestCLI_HelpExitCode(t *testing.T) {
	dir := t.TempDir()

	// No args: should print usage and exit 0.
	_, _, code := runKnown(t, dir)
	if code != 0 {
		t.Fatalf("known (no args) should exit 0, got %d", code)
	}

	// --help: should exit 0.
	_, _, code = runKnown(t, dir, "--help")
	if code != 0 {
		t.Fatalf("known --help should exit 0, got %d", code)
	}

	// Bogus subcommand: should exit 1.
	_, _, code = runKnown(t, dir, "bogus")
	if code != 1 {
		t.Fatalf("known bogus should exit 1, got %d", code)
	}
}

func TestCLI_InitNoScaffold(t *testing.T) {
	dir := t.TempDir()

	_, stderr, code := runKnown(t, dir, "init", "--no-scaffold")
	if code != 0 {
		t.Fatalf("known init --no-scaffold failed (exit %d): %s", code, stderr)
	}

	// .known.yaml must exist.
	if _, err := os.Stat(filepath.Join(dir, ".known.yaml")); err != nil {
		t.Fatalf(".known.yaml not created: %v", err)
	}

	// .claude/ directory must NOT exist.
	claudeDir := filepath.Join(dir, ".claude")
	if _, err := os.Stat(claudeDir); err == nil {
		t.Fatalf(".claude/ directory should NOT exist after --no-scaffold, but it does")
	}
}

func TestCLI_InitIdempotent(t *testing.T) {
	dir := t.TempDir()

	// First init (with scaffold).
	initKnown(t, dir)

	// Modify a skill file to check it survives re-init.
	skillPath := filepath.Join(dir, ".claude", "skills", "remember", "SKILL.md")
	marker := "# CUSTOM MODIFICATION\nThis line was added by the test.\n"
	if err := os.WriteFile(skillPath, []byte(marker), 0o644); err != nil {
		t.Fatalf("write marker to skill file: %v", err)
	}

	// Re-init with --force (overwrites .known.yaml but scaffold should skip
	// existing files).
	_, stderr, code := runKnown(t, dir, "init", "--force")
	if code != 0 {
		t.Fatalf("known init --force failed (exit %d): %s", code, stderr)
	}

	// Verify the skill file still has our custom content.
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill file after re-init: %v", err)
	}
	if string(data) != marker {
		t.Fatalf("skill file was overwritten by re-init; expected custom marker, got:\n%s", string(data))
	}
}
