package cmd

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// TestPrimeDoc_Embedded guards the generated prime.md contract: CLI-first
// guidance with no slash-command leakage from the plugin source.
func TestPrimeDoc_Embedded(t *testing.T) {
	for _, want := range []string{
		"# Known — Persistent Memory Graph for LLMs",
		"## Commands",
		"`known add",
		"`known recall",
		"--supersedes",
		"## Behavior",
	} {
		if !strings.Contains(primeDoc, want) {
			t.Errorf("primeDoc missing %q", want)
		}
	}

	if strings.Contains(primeDoc, "/known:") {
		t.Error("primeDoc contains /known: slash references; generatePrime must not leak plugin syntax")
	}
	if strings.Contains(primeDoc, "## Status") {
		t.Error("primeDoc contains a static Status section; status must be appended live")
	}
}

func TestPrintPrimeStatus(t *testing.T) {
	repo := &recallEntryRepo{listEntries: []model.Entry{{}, {}}}
	var out, stderr bytes.Buffer
	app := &App{
		Entries: repo,
		Scopes:  &stubScopeRepo{scopes: []model.Scope{{Path: "myproj"}, {Path: "myproj.api"}}},
		Printer: NewPrinter(&out, false, false),
		Stderr:  &stderr,
		Config:  &AppConfig{DefaultScope: "myproj"},
	}

	printPrimeStatus(context.Background(), app)

	got := out.String()
	for _, want := range []string{
		"## Status",
		"Scope  myproj — 2 entries",
		"Graph  2 entries across 2 scopes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q\ngot:\n%s", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

// failingListRepo forces the entry listing to fail so the status section is
// skipped and downgraded to a stderr note.
type failingListRepo struct{ recallEntryRepo }

func (f *failingListRepo) List(context.Context, storage.EntryFilter) ([]model.Entry, error) {
	return nil, errors.New("boom")
}

func TestPrintPrimeStatus_StorageErrorSkipsSection(t *testing.T) {
	var out, stderr bytes.Buffer
	app := &App{
		Entries: &failingListRepo{},
		Scopes:  &stubScopeRepo{},
		Printer: NewPrinter(&out, false, false),
		Stderr:  &stderr,
		Config:  &AppConfig{DefaultScope: "myproj"},
	}

	printPrimeStatus(context.Background(), app)

	if out.Len() != 0 {
		t.Errorf("status section printed despite storage error:\n%s", out.String())
	}
	if !strings.Contains(stderr.String(), "graph status unavailable") {
		t.Errorf("stderr missing unavailability note, got: %q", stderr.String())
	}
}

// TestPrimeSubcommand_E2E runs the full dispatch path against a temp SQLite
// DB: guidance plus a live (empty-graph) status footer, exit 0. No embedder
// is required, so this stays hermetic.
func TestPrimeSubcommand_E2E(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "prime.db")

	cap := captureStdout(t)
	code := Run(context.Background(), []string{"--dsn", dsn, "prime"})
	got := cap.restore()

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	for _, want := range []string{
		"# Known — Persistent Memory Graph for LLMs",
		"## Commands",
		"## Status",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prime output missing %q", want)
		}
	}
}
