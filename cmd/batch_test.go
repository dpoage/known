package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/known/model"
)

// newBatchTestApp constructs a minimal App suitable for batch tests.
func newBatchTestApp(repo *stubEntryRepo) *App {
	return &App{
		DB:       &stubBackend{},
		Entries:  repo,
		Embedder: &stubEmbedder{dims: 3},
		Printer:  NewPrinter(&bytes.Buffer{}, false, true),
		Stderr:   &bytes.Buffer{},
		Config: &AppConfig{
			DefaultScope:     "root",
			MaxContentLength: model.MaxContentLength,
			DefaultTTL: map[model.SourceType]time.Duration{
				model.SourceConversation: 7 * 24 * time.Hour,
				model.SourceManual:       90 * 24 * time.Hour,
			},
		},
	}
}

func TestRunAddBatch_BasicJSONL(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	// Feed JSONL via stdin.
	entries := []batchEntry{
		{Content: "fact one", Title: "First"},
		{Content: "fact two", Scope: "backend"},
		{Content: "fact three", SourceType: "file", SourceRef: "main.go"},
	}

	r, w, _ := os.Pipe()
	for _, e := range entries {
		data, _ := json.Marshal(e)
		w.Write(data)
		w.Write([]byte("\n"))
	}
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, nil)
	if err != nil {
		t.Fatalf("runAddBatch: %v", err)
	}

	// stubEntryRepo.CreateOrUpdate only captures the last one, but the
	// function should complete without error for all 3.
	if repo.created == nil {
		t.Fatal("expected entries to be created")
	}
}

func TestRunAddBatch_EmptyStdin(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, nil)
	if err == nil {
		t.Fatal("expected error for empty stdin")
	}
	if !strings.Contains(err.Error(), "no entries") {
		t.Errorf("error %q should mention 'no entries'", err.Error())
	}
}

func TestRunAddBatch_InvalidJSON(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	w.Write([]byte("not json\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error %q should mention 'invalid JSON'", err.Error())
	}
}

func TestRunAddBatch_MissingContent(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	data, _ := json.Marshal(batchEntry{Title: "no content"})
	w.Write(data)
	w.Write([]byte("\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, nil)
	if err == nil {
		t.Fatal("expected error for missing content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("error %q should mention 'content is required'", err.Error())
	}
}

func TestRunAddBatch_DryRun(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	data, _ := json.Marshal(batchEntry{Content: "dry run fact"})
	w.Write(data)
	w.Write([]byte("\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, []string{"--dry-run"})
	if err != nil {
		t.Fatalf("runAddBatch --dry-run: %v", err)
	}

	if repo.created != nil {
		t.Error("dry run should not create entries")
	}
}

func TestRunAddBatch_ContentTooLong(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	longContent := strings.Repeat("x", model.MaxContentLength+1)
	r, w, _ := os.Pipe()
	data, _ := json.Marshal(batchEntry{Content: longContent})
	w.Write(data)
	w.Write([]byte("\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, nil)
	if err == nil {
		t.Fatal("expected error for content too long")
	}
	if !strings.Contains(err.Error(), "exceeds maximum length") {
		t.Errorf("error %q should mention 'exceeds maximum length'", err.Error())
	}
}

func TestRunAdd_BatchDelegates(t *testing.T) {
	// Verify that runAdd with --batch delegates to runAddBatch.
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	data, _ := json.Marshal(batchEntry{Content: "delegated fact"})
	w.Write(data)
	w.Write([]byte("\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAdd(context.Background(), app, []string{"--batch"})
	if err != nil {
		t.Fatalf("runAdd --batch: %v", err)
	}
}

func TestRunAddBatch_DefaultScope(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newBatchTestApp(repo)

	r, w, _ := os.Pipe()
	data, _ := json.Marshal(batchEntry{Content: "scoped fact"})
	w.Write(data)
	w.Write([]byte("\n"))
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := runAddBatch(context.Background(), app, []string{"--scope", "custom"})
	if err != nil {
		t.Fatalf("runAddBatch: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected entry to be created")
	}
	if repo.created.Scope != "custom" {
		t.Errorf("scope = %q, want %q", repo.created.Scope, "custom")
	}
}
