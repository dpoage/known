package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
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
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: &stubSuggestEmbedder{},
		Engine:   query.New(db.Entries(), db.Edges(), &stubSuggestEmbedder{}),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: model.RootScope},
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
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: &stubSuggestEmbedder{},
		Engine:   query.New(db.Entries(), db.Edges(), &stubSuggestEmbedder{}),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: model.RootScope},
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
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: &stubSuggestEmbedder{},
		Engine:   query.New(db.Entries(), db.Edges(), &stubSuggestEmbedder{}),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: model.RootScope},
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

// dominanceEmbedder always returns [1, 0, 0, 0] so entries stored with that
// vector score 1.0 (cosine distance 0) and entries stored with an orthogonal
// component score predictably lower — exercising the Top-1 dominance branch
// (score ≥ minScore AND margin ≥ minMargin) without a real embedder.
type dominanceEmbedder struct{}

func (d *dominanceEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 0, 0, 0}, nil
}
func (d *dominanceEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}
func (d *dominanceEmbedder) Dimensions() int   { return 4 }
func (d *dominanceEmbedder) ModelName() string { return "dominance-stub" }

// TestResolveEntry_SemanticDominance verifies that resolveEntryConfident
// resolves via the Top-1 semantic dominance path (score ≥ 0.80, margin ≥ 0.10)
// without touching the text-search fallback.
//
// Setup:
//   - Entry A stored with vec [1,0,0,0] → cosine(query,A) = 1.0 → score 1.0
//   - Entry B stored with vec [0.6,0.8,0,0] → cosine(query,B) = 0.6 → score 0.6
//   - Query "renderer design" (no substring match in content) forces semantic path.
//
// The dominance rule fires: score(A)=1.0 ≥ 0.80 AND margin=0.4 ≥ 0.10.
// Mutating minScore to 1.01 would make the check fail and return an ambiguous error.
func TestResolveEntry_SemanticDominance(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	src := model.Source{Type: model.SourceManual}

	// Entry A: stored with query vector — will score 1.0.
	eA := model.NewEntry("Deferred shading pipeline architecture", src)
	eA.Scope = model.RootScope
	eA = eA.WithEmbedding([]float32{1, 0, 0, 0}, "dominance-stub")
	if _, err := db.Entries().CreateOrUpdate(ctx, &eA); err != nil {
		t.Fatal(err)
	}

	// Entry B: stored with a lower-similarity vector — will score 0.6.
	// cos([1,0,0,0], [0.6,0.8,0,0]) = 0.6 (unit vectors; dot = 0.6).
	eB := model.NewEntry("Auth token refresh strategy", src)
	eB.Scope = model.RootScope
	eB = eB.WithEmbedding([]float32{0.6, 0.8, 0, 0}, "dominance-stub")
	if _, err := db.Entries().CreateOrUpdate(ctx, &eB); err != nil {
		t.Fatal(err)
	}

	emb := &dominanceEmbedder{}
	var buf bytes.Buffer
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: emb,
		Engine:   query.New(db.Entries(), db.Edges(), emb),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: model.RootScope},
	}

	// Query has no substring overlap with either entry's content, so text
	// fallback is bypassed and resolution relies entirely on dominance.
	got, err := resolveEntry(ctx, app, "renderer design")
	if err != nil {
		t.Fatalf("expected dominant resolution, got error: %v", err)
	}
	if got != eA.ID {
		t.Errorf("dominance resolution: got %v, want entry A (%v)", got, eA.ID)
	}
	out := buf.String()
	if !strings.Contains(out, "Resolved") {
		t.Errorf("expected 'Resolved' in output, got: %s", out)
	}
}
