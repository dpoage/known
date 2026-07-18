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

// --- helpers ------------------------------------------------------------------

func newLinkTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	stub := &stubSuggestEmbedder{}
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: stub,
		Engine:   query.New(db.Entries(), db.Edges(), stub),
		Printer:  NewPrinter(&bytes.Buffer{}, false, false),
		Stderr:   &bytes.Buffer{},
		Config:   &AppConfig{DefaultScope: model.RootScope},
	}
	return app, func() { db.Close() }
}

func addTestEntry(t *testing.T, app *App, content string) model.Entry {
	t.Helper()
	ctx := context.Background()
	src := model.Source{Type: model.SourceManual}
	e := model.NewEntry(content, src)
	e.Scope = model.RootScope
	result, err := app.Entries.CreateOrUpdate(ctx, &e)
	if err != nil {
		t.Fatalf("addTestEntry(%q): %v", content, err)
	}
	return *result
}

// --- content-addressable link -------------------------------------------------

func TestRunLink_ContentAddressable(t *testing.T) {
	ctx := context.Background()
	app, cleanup := newLinkTestApp(t)
	defer cleanup()

	eA := addTestEntry(t, app, "Renderer uses deferred shading pipeline")
	eB := addTestEntry(t, app, "Deferred shading requires G-buffer allocation")

	var buf bytes.Buffer
	app.Printer = NewPrinter(&buf, false, false)

	// Link by content query — no ULIDs.
	err := runLink(ctx, app, []string{"deferred shading pipeline", "G-buffer allocation"})
	if err != nil {
		t.Fatalf("runLink by content: %v", err)
	}

	// Verify edge was persisted.
	edges, err := app.Edges.EdgesFrom(ctx, eA.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].ToID != eB.ID {
		t.Errorf("edge target: got %s, want %s", edges[0].ToID, eB.ID)
	}
}

func TestRunLink_ULID(t *testing.T) {
	ctx := context.Background()
	app, cleanup := newLinkTestApp(t)
	defer cleanup()

	eA := addTestEntry(t, app, "Dependency injection pattern")
	eB := addTestEntry(t, app, "Service locator anti-pattern")

	err := runLink(ctx, app, []string{eA.ID.String(), eB.ID.String(), "--type", "contradicts"})
	if err != nil {
		t.Fatalf("runLink by ULID: %v", err)
	}

	edges, err := app.Edges.EdgesFrom(ctx, eA.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Type != model.EdgeContradicts {
		t.Errorf("expected contradicts edge, got %+v", edges)
	}
}

func TestRunLink_Ambiguous(t *testing.T) {
	ctx := context.Background()
	app, cleanup := newLinkTestApp(t)
	defer cleanup()

	var buf bytes.Buffer
	app.Printer = NewPrinter(&buf, false, false)

	addTestEntry(t, app, "Auth token expiry policy")
	addTestEntry(t, app, "Auth middleware validation")
	_ = addTestEntry(t, app, "Unrelated entry about database indexing")

	// Ambiguous query for from-entry; should fail.
	err := runLink(ctx, app, []string{"auth", "database indexing"})
	if err == nil {
		t.Fatal("expected error for ambiguous from-entry")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Multiple entries") {
		t.Errorf("expected candidate list in output, got:\n%s", out)
	}
}

func TestRunLink_NoMatch(t *testing.T) {
	ctx := context.Background()
	app, cleanup := newLinkTestApp(t)
	defer cleanup()

	addTestEntry(t, app, "Some entry about rendering")

	err := runLink(ctx, app, []string{"quantum chromodynamics", "rendering"})
	if err == nil {
		t.Fatal("expected error for no-match query")
	}
	if !strings.Contains(err.Error(), "no entries matching") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunLink_EmptyEdgeType(t *testing.T) {
	ctx := context.Background()
	app, cleanup := newLinkTestApp(t)
	defer cleanup()

	eA := addTestEntry(t, app, "Entry Alpha")
	eB := addTestEntry(t, app, "Entry Beta")

	// Empty edge type should fail validation.
	err := runLink(ctx, app, []string{eA.ID.String(), eB.ID.String(), "--type", ""})
	if err == nil {
		t.Fatal("expected error for empty edge type")
	}
	if !strings.Contains(err.Error(), "edge type") {
		t.Errorf("error should mention edge type, got: %v", err)
	}
}

// --- accept subcommand --------------------------------------------------------

// stubSuggestEngine wraps a query.Engine with a mock entry/edge repo so
// SuggestLinks returns predictable results without a real embedder.
func newStubAcceptApp(t *testing.T) (*App, model.Entry, []model.Entry) {
	t.Helper()
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}

	// Stub embedder that maps text → unit vector deterministically.
	stub := &stubSuggestEmbedder{}

	eng := query.New(db.Entries(), db.Edges(), stub)

	var buf bytes.Buffer
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: stub,
		Engine:   eng,
		Printer:  NewPrinter(&buf, false, false),
		Stderr:   &bytes.Buffer{},
		Config:   &AppConfig{DefaultScope: model.RootScope},
	}

	// Create source entry with embedding.
	src := model.Source{Type: model.SourceManual}
	eSource := model.NewEntry("Renderer architecture decision", src)
	eSource.Scope = model.RootScope
	eSource = eSource.WithEmbedding([]float32{1, 0, 0, 0}, "stub")
	stored, err := app.Entries.CreateOrUpdate(ctx, &eSource)
	if err != nil {
		t.Fatal(err)
	}
	eSource = *stored

	// Create neighbor entries with embeddings close to source.
	neighbors := make([]model.Entry, 3)
	vecs := [][]float32{
		{0.9, 0.436, 0, 0}, // close
		{0.8, 0.6, 0, 0},   // medium
		{0, 0, 1, 0},       // far
	}
	for i, v := range vecs {
		e := model.NewEntry("Neighbor entry "+string(rune('A'+i)), src)
		e.Scope = model.RootScope
		e = e.WithEmbedding(v, "stub")
		result, err2 := app.Entries.CreateOrUpdate(ctx, &e)
		if err2 != nil {
			t.Fatal(err2)
		}
		neighbors[i] = *result
	}

	// Re-fetch source so it carries the stored ID.
	t.Cleanup(func() { db.Close() })
	return app, eSource, neighbors
}

// stubSuggestEmbedder is a minimal Embedder that returns zeros (unused in
// SuggestLinks since the entry already carries its embedding vector).
type stubSuggestEmbedder struct{}

func (s *stubSuggestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0, 0, 0, 0}, nil
}
func (s *stubSuggestEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0, 0, 0, 0}
	}
	return out, nil
}
func (s *stubSuggestEmbedder) Dimensions() int   { return 4 }
func (s *stubSuggestEmbedder) ModelName() string { return "stub" }

func TestRunLinkAccept_ByIndex(t *testing.T) {
	ctx := context.Background()
	app, source, neighbors := newStubAcceptApp(t)

	err := runLinkAccept(ctx, app, []string{source.ID.String(), "1"})
	if err != nil {
		t.Fatalf("runLinkAccept index 1: %v", err)
	}

	edges, err := app.Edges.EdgesFrom(ctx, source.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	// The closest neighbor (index 1) should be neighbors[0] (vec closest to source).
	if edges[0].ToID != neighbors[0].ID {
		t.Errorf("expected closest neighbor, got toID=%s", edges[0].ToID)
	}
}

func TestRunLinkAccept_All(t *testing.T) {
	ctx := context.Background()
	app, source, _ := newStubAcceptApp(t)

	err := runLinkAccept(ctx, app, []string{source.ID.String(), "--all"})
	if err != nil {
		t.Fatalf("runLinkAccept --all: %v", err)
	}

	edges, err := app.Edges.EdgesFrom(ctx, source.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) < 2 {
		t.Errorf("expected at least 2 edges with --all, got %d", len(edges))
	}
}

func TestRunLinkAccept_ContentQuery(t *testing.T) {
	ctx := context.Background()
	app, _, _ := newStubAcceptApp(t)

	// Accept by content query instead of ULID.
	err := runLinkAccept(ctx, app, []string{"Renderer architecture decision", "1"})
	if err != nil {
		t.Fatalf("runLinkAccept by content: %v", err)
	}

	// At least one edge should have been created.
	results, err := app.Entries.List(ctx, storage.EntryFilter{Scope: model.RootScope})
	if err != nil {
		t.Fatal(err)
	}

	var totalEdges int
	for _, e := range results {
		edges, err2 := app.Edges.EdgesFrom(ctx, e.ID, storage.EdgeFilter{})
		if err2 != nil {
			t.Fatal(err2)
		}
		totalEdges += len(edges)
	}
	if totalEdges == 0 {
		t.Error("expected at least one edge to be created via content query accept")
	}
}

func TestRunLinkAccept_InvalidIndex(t *testing.T) {
	ctx := context.Background()
	app, source, _ := newStubAcceptApp(t)

	err := runLinkAccept(ctx, app, []string{source.ID.String(), "99"})
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
	if !strings.Contains(err.Error(), "invalid index") {
		t.Errorf("expected invalid index error, got: %v", err)
	}
}

func TestRunLinkAccept_NoIndicesShowsSuggestions(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	app, source, _ := newStubAcceptApp(t)
	app.Printer = NewPrinter(&buf, false, false)

	err := runLinkAccept(ctx, app, []string{source.ID.String()})
	if err == nil {
		t.Fatal("expected error when no indices provided")
	}

	out := buf.String()
	if !strings.Contains(out, "Suggestions") {
		t.Errorf("expected suggestions list in output, got:\n%s", out)
	}
}

// TestRunLinkAccept_CrossScope verifies that `known link accept` resolves
// and creates an edge for a suggestion whose entry lives in a different
// scope from the source entry (known-lxj: SuggestLinks now searches
// globally, so cross-scope candidates must be acceptable too).
func TestRunLinkAccept_CrossScope(t *testing.T) {
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	stub := &stubSuggestEmbedder{}
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: stub,
		Engine:   query.New(db.Entries(), db.Edges(), stub),
		Printer:  NewPrinter(&bytes.Buffer{}, false, false),
		Stderr:   &bytes.Buffer{},
		Config:   &AppConfig{DefaultScope: model.RootScope},
	}

	src := model.Source{Type: model.SourceManual}

	eSource := model.NewEntry("Renderer architecture decision", src)
	eSource.Scope = "projectA"
	eSource = eSource.WithEmbedding([]float32{1, 0, 0, 0}, "stub")
	storedSource, err := app.Entries.CreateOrUpdate(ctx, &eSource)
	if err != nil {
		t.Fatal(err)
	}
	eSource = *storedSource

	// Neighbor in a DIFFERENT scope, closer than any same-scope candidate.
	eCross := model.NewEntry("Renderer architecture notes", src)
	eCross.Scope = "projectB"
	eCross = eCross.WithEmbedding([]float32{0.9, 0.436, 0, 0}, "stub")
	storedCross, err := app.Entries.CreateOrUpdate(ctx, &eCross)
	if err != nil {
		t.Fatal(err)
	}
	eCross = *storedCross

	if err := runLinkAccept(ctx, app, []string{eSource.ID.String(), "1"}); err != nil {
		t.Fatalf("runLinkAccept cross-scope: %v", err)
	}

	edges, err := app.Edges.EdgesFrom(ctx, eSource.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].ToID != eCross.ID {
		t.Fatalf("expected edge to cross-scope neighbor %s, got toID=%s", eCross.ID, edges[0].ToID)
	}

	// Confirm the linked entry is genuinely in a different scope (proves the
	// accept path did not silently re-filter by scope anywhere).
	target, err := app.Entries.Get(ctx, edges[0].ToID)
	if err != nil {
		t.Fatal(err)
	}
	if target.Scope == eSource.Scope {
		t.Fatalf("expected cross-scope target, both entries ended up in scope %q", target.Scope)
	}
	if target.Scope != "projectB" {
		t.Errorf("expected linked entry scope %q, got %q", "projectB", target.Scope)
	}
}
