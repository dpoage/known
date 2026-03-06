package cmd

import (
	"bytes"
	"context"
	"testing"

	"strings"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

var manualSource = model.Source{Type: model.SourceManual}

func TestResolveEntry_ULID(t *testing.T) {
	app := &App{
		Printer: NewPrinter(&bytes.Buffer{}, false, false),
		Config:  &AppConfig{DefaultScope: model.RootScope},
	}

	want := model.NewID()
	got, err := resolveEntry(context.Background(), app, want.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("resolveEntry() = %v, want %v", got, want)
	}
}

func TestResolveEntry_TextSingleMatch(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	entry := model.NewEntry("The rate limit is 100 req/s per tenant", manualSource)
	entry.Title = "Rate Limiting"
	entry.Scope = model.RootScope
	if err := db.Entries().Create(ctx, &entry); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Printer: NewPrinter(&buf, false, false),
		Config:  &AppConfig{DefaultScope: model.RootScope},
	}

	got, err := resolveEntry(ctx, app, "rate limit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != entry.ID {
		t.Errorf("resolveEntry() = %v, want %v", got, entry.ID)
	}
}

func TestResolveEntry_TextNoMatch(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Printer: NewPrinter(&buf, false, false),
		Config:  &AppConfig{DefaultScope: model.RootScope},
	}

	_, err = resolveEntry(ctx, app, "nonexistent thing")
	if err == nil {
		t.Fatal("expected error for no matches")
	}
}

func TestResolveEntry_TextAmbiguous(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	e1 := model.NewEntry("Auth tokens expire after 24 hours", manualSource)
	e1.Title = "Auth Tokens"
	e1.Scope = model.RootScope
	e2 := model.NewEntry("Auth middleware requires Bearer tokens", manualSource)
	e2.Title = "Auth Middleware"
	e2.Scope = model.RootScope

	if err := db.Entries().Create(ctx, &e1); err != nil {
		t.Fatal(err)
	}
	if err := db.Entries().Create(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Printer: NewPrinter(&buf, false, false),
		Config:  &AppConfig{DefaultScope: model.RootScope},
	}

	_, err = resolveEntry(ctx, app, "auth")
	if err == nil {
		t.Fatal("expected error for ambiguous matches")
	}

	output := buf.String()
	if !strings.Contains(output, "Auth Tokens") || !strings.Contains(output, "Auth Middleware") || !strings.Contains(output, "Multiple entries") {
		t.Errorf("ambiguous output should list candidates, got:\n%s", output)
	}
}

func TestSearchByText_ScopeLimited(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	// Entry in scope "backend"
	e1 := model.NewEntry("Backend rate limit config", manualSource)
	e1.Scope = "backend"
	scope := model.NewScope("backend")
	if err := db.Scopes().Upsert(ctx, &scope); err != nil {
		t.Fatal(err)
	}
	if err := db.Entries().Create(ctx, &e1); err != nil {
		t.Fatal(err)
	}

	// Entry in scope "frontend"
	e2 := model.NewEntry("Frontend rate limit display", manualSource)
	e2.Scope = "frontend"
	scope2 := model.NewScope("frontend")
	if err := db.Scopes().Upsert(ctx, &scope2); err != nil {
		t.Fatal(err)
	}
	if err := db.Entries().Create(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Printer: NewPrinter(&bytes.Buffer{}, false, false),
		Config:  &AppConfig{DefaultScope: "backend"},
	}

	matches, err := searchByText(ctx, app, "rate limit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match scoped to backend, got %d", len(matches))
	}
	if matches[0].ID != e1.ID {
		t.Errorf("expected backend entry, got %s", matches[0].Scope)
	}
}

// Verify stub interfaces still satisfy storage.EntryRepo.
var _ storage.EntryRepo = (*stubEntryRepo)(nil)
