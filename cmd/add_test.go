package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// ---------------------------------------------------------------------------
// Minimal mocks for runAdd tests
// ---------------------------------------------------------------------------

// stubEntryRepo captures the entry passed to CreateOrUpdate so tests can
// inspect it. Only the methods used by runAdd are implemented.
// Pre-seed entries in the `existing` map for Get lookups (used by --link).
type stubEntryRepo struct {
	created         *model.Entry
	existing        map[model.ID]*model.Entry
	versionToReturn int // when > 1, simulates a dedup hit (Version > 1)
}

func (s *stubEntryRepo) CreateOrUpdate(_ context.Context, entry *model.Entry) (*model.Entry, error) {
	clone := *entry
	if s.versionToReturn > 1 {
		clone.Version = s.versionToReturn
	}
	s.created = &clone
	return &clone, nil
}
func (s *stubEntryRepo) Create(context.Context, *model.Entry) error { return nil }
func (s *stubEntryRepo) Get(_ context.Context, id model.ID) (*model.Entry, error) {
	if s.existing != nil {
		if e, ok := s.existing[id]; ok {
			return e, nil
		}
	}
	return nil, storage.ErrNotFound
}
func (s *stubEntryRepo) Update(context.Context, *model.Entry) error { return nil }
func (s *stubEntryRepo) Delete(context.Context, model.ID) error     { return nil }
func (s *stubEntryRepo) List(context.Context, storage.EntryFilter) ([]model.Entry, error) {
	return nil, nil
}
func (s *stubEntryRepo) SearchSimilar(context.Context, []float32, string, storage.SimilarityMetric, int) ([]storage.SimilarityResult, error) {
	return nil, nil
}
func (s *stubEntryRepo) DeleteExpired(context.Context) (int64, error) { return 0, nil }
func (s *stubEntryRepo) SearchText(context.Context, string, string, int) ([]storage.SimilarityResult, error) {
	return nil, nil
}

// stubEdgeRepo captures created edges for inspection.
type stubEdgeRepo struct {
	created []model.Edge
}

func (s *stubEdgeRepo) Create(_ context.Context, edge *model.Edge) error {
	s.created = append(s.created, *edge)
	return nil
}
func (s *stubEdgeRepo) Get(context.Context, model.ID) (*model.Edge, error) {
	return nil, storage.ErrNotFound
}
func (s *stubEdgeRepo) Update(context.Context, *model.Edge) error { return nil }
func (s *stubEdgeRepo) Delete(context.Context, model.ID) error    { return nil }
func (s *stubEdgeRepo) EdgesFrom(context.Context, model.ID, storage.EdgeFilter) ([]model.Edge, error) {
	return nil, nil
}
func (s *stubEdgeRepo) EdgesTo(context.Context, model.ID, storage.EdgeFilter) ([]model.Edge, error) {
	return nil, nil
}
func (s *stubEdgeRepo) EdgesBetween(context.Context, model.ID, model.ID) ([]model.Edge, error) {
	return nil, nil
}
func (s *stubEdgeRepo) FindConflicts(context.Context, model.ID) ([]model.Entry, error) {
	return nil, nil
}

// stubEmbedder returns a fixed-dimension zero vector.
type stubEmbedder struct{ dims int }

func (e *stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, e.dims), nil
}
func (e *stubEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v, err := e.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
func (e *stubEmbedder) Dimensions() int   { return e.dims }
func (e *stubEmbedder) ModelName() string { return "stub" }

// newTestApp constructs a minimal App suitable for calling runAdd.
func newTestApp(repo *stubEntryRepo) *App {
	return &App{
		DB:       &stubBackend{},
		Entries:  repo,
		Embedder: &stubEmbedder{dims: 3},
		Printer:  NewPrinter(&bytes.Buffer{}, false, true),
		Stderr:   &bytes.Buffer{},
		Config: &AppConfig{
			DefaultScope:     "root",
			MaxContentLength: model.MaxContentLength,
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRunAdd_TTLZero_PermanentEntry(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--ttl", "0", "permanent fact"}, "add")
	if err != nil {
		t.Fatalf("runAdd with --ttl 0: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}

	if repo.created.TTL != nil {
		t.Errorf("TTL should be nil for --ttl 0, got %v", repo.created.TTL)
	}
	if repo.created.ExpiresAt != nil {
		t.Errorf("ExpiresAt should be nil for --ttl 0, got %v", repo.created.ExpiresAt)
	}
	if repo.created.IsExpired() {
		t.Error("entry with --ttl 0 should not be expired")
	}
}

func TestRunAdd_TTLZero_ExplicitPermanent(t *testing.T) {
	// --ttl 0 is accepted for compat; results in no expiry (same as omitting --ttl).
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{
		"--ttl", "0",
		"--source-type", "conversation",
		"explicitly permanent entry",
	}, "add")
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}
	if repo.created.TTL != nil {
		t.Errorf("TTL should be nil for --ttl 0, got %v", repo.created.TTL)
	}
	if repo.created.ExpiresAt != nil {
		t.Errorf("ExpiresAt should be nil for --ttl 0, got %v", repo.created.ExpiresAt)
	}
}

func TestRunAdd_NoTTLFlag_PermanentEntry(t *testing.T) {
	// Without --ttl, unflagged adds are permanent: no TTL, no ExpiresAt.
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	for _, st := range []string{"manual", "conversation", "file", "url"} {
		repo.created = nil
		err := runAdd(context.Background(), app, []string{"--source-type", st, "fact without ttl"}, "add")
		if err != nil {
			t.Fatalf("runAdd (source-type=%s): %v", st, err)
		}
		if repo.created == nil {
			t.Fatalf("source-type=%s: expected entry to be created", st)
		}
		if repo.created.TTL != nil {
			t.Errorf("source-type=%s: TTL should be nil (permanent by default), got %v", st, repo.created.TTL)
		}
		if repo.created.ExpiresAt != nil {
			t.Errorf("source-type=%s: ExpiresAt should be nil (permanent by default), got %v", st, repo.created.ExpiresAt)
		}
	}
}

func TestRunAdd_InvalidTTL(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--ttl", "notaduration", "bad ttl"}, "add")
	if err == nil {
		t.Fatal("expected error for invalid TTL")
	}
	if !strings.Contains(err.Error(), "invalid ttl") {
		t.Errorf("error %q should contain 'invalid ttl'", err.Error())
	}
}

func TestRunAdd_MissingContent(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{}, "add")
	if err == nil {
		t.Fatal("expected error for missing content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("error %q should mention content required", err.Error())
	}
}

func TestRunAdd_LinkCreatesEdge(t *testing.T) {
	// Pre-seed an existing entry that the link will target.
	targetID := model.NewID()
	targetEntry := model.NewEntry("target content", model.Source{Type: model.SourceManual, Reference: "test"})
	targetEntry.ID = targetID
	targetEntry.Scope = "root"

	repo := &stubEntryRepo{
		existing: map[model.ID]*model.Entry{targetID: &targetEntry},
	}
	edgeRepo := &stubEdgeRepo{}
	app := newTestApp(repo)
	app.Edges = edgeRepo

	err := runAdd(context.Background(), app, []string{
		"--link", "elaborates:" + targetID.String(),
		"detail about the target",
	}, "add")
	if err != nil {
		t.Fatalf("runAdd with --link: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}
	if len(edgeRepo.created) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edgeRepo.created))
	}

	edge := edgeRepo.created[0]
	if edge.FromID != repo.created.ID {
		t.Errorf("edge from = %s, want %s (new entry)", edge.FromID, repo.created.ID)
	}
	if edge.ToID != targetID {
		t.Errorf("edge to = %s, want %s (target)", edge.ToID, targetID)
	}
	if edge.Type != model.EdgeElaborates {
		t.Errorf("edge type = %s, want elaborates", edge.Type)
	}
}

func TestRunAdd_LinkMultiple(t *testing.T) {
	id1, id2 := model.NewID(), model.NewID()
	e1 := model.NewEntry("e1", model.Source{Type: model.SourceManual, Reference: "test"})
	e1.ID = id1
	e1.Scope = "root"
	e2 := model.NewEntry("e2", model.Source{Type: model.SourceManual, Reference: "test"})
	e2.ID = id2
	e2.Scope = "root"

	repo := &stubEntryRepo{
		existing: map[model.ID]*model.Entry{id1: &e1, id2: &e2},
	}
	edgeRepo := &stubEdgeRepo{}
	app := newTestApp(repo)
	app.Edges = edgeRepo

	err := runAdd(context.Background(), app, []string{
		"--link", "elaborates:" + id1.String(),
		"--link", "depends-on:" + id2.String(),
		"entry with two links",
	}, "add")
	if err != nil {
		t.Fatalf("runAdd with multiple --link: %v", err)
	}

	if len(edgeRepo.created) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edgeRepo.created))
	}
	if edgeRepo.created[0].Type != model.EdgeElaborates {
		t.Errorf("first edge type = %s, want elaborates", edgeRepo.created[0].Type)
	}
	if edgeRepo.created[1].Type != model.EdgeDependsOn {
		t.Errorf("second edge type = %s, want depends-on", edgeRepo.created[1].Type)
	}
}

func TestRunAdd_LinkInvalidFormat(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{
		"--link", "no-colon-here",
		"some content",
	}, "add")
	if err == nil {
		t.Fatal("expected error for invalid link format")
	}
	if !strings.Contains(err.Error(), "invalid --link format") {
		t.Errorf("error %q should mention invalid format", err.Error())
	}
}

func TestRunAdd_LinkBadTarget(t *testing.T) {
	// Target doesn't exist — should error.
	repo := &stubEntryRepo{}
	edgeRepo := &stubEdgeRepo{}
	app := newTestApp(repo)
	app.Edges = edgeRepo

	fakeID := model.NewID()
	err := runAdd(context.Background(), app, []string{
		"--link", "elaborates:" + fakeID.String(),
		"orphan link attempt",
	}, "add")
	if err == nil {
		t.Fatal("expected error for non-existent target")
	}
	if !strings.Contains(err.Error(), "target entry") {
		t.Errorf("error %q should mention target entry", err.Error())
	}
}

// Verify interface compliance of stubs at compile time.
var _ storage.EntryRepo = (*stubEntryRepo)(nil)
var _ storage.EdgeRepo = (*stubEdgeRepo)(nil)

// ---------------------------------------------------------------------------
// New tests: input parsing, output contract, error suggestion, dedup
// ---------------------------------------------------------------------------

func TestRunAdd_MultiWordContent_NoQuotes(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"some", "fact", "without", "quotes"}, "add")
	if err != nil {
		t.Fatalf("runAdd multi-word: %v", err)
	}
	if repo.created == nil {
		t.Fatal("expected entry")
	}
	if repo.created.Content != "some fact without quotes" {
		t.Errorf("content = %q, want %q", repo.created.Content, "some fact without quotes")
	}
}

func TestRunAdd_MultiWordContent_FlagsAfter(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"a", "fact", "--scope", "myproj"}, "add")
	if err != nil {
		t.Fatalf("runAdd multi-word with flags: %v", err)
	}
	if repo.created == nil {
		t.Fatal("expected entry")
	}
	if repo.created.Content != "a fact" {
		t.Errorf("content = %q, want %q", repo.created.Content, "a fact")
	}
}

func TestRunAdd_ContentEmpty_Error(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--scope", "root"}, "add")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("error %q should mention content required", err.Error())
	}
}

func TestRunAdd_ContentOversized_Error(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)
	// MaxContentLength is 4096 by default in newTestApp
	oversized := strings.Repeat("x", 4097)
	err := runAdd(context.Background(), app, []string{oversized}, "add")
	if err == nil {
		t.Fatal("expected error for oversized content")
	}
	if !strings.Contains(err.Error(), "exceeds maximum length") {
		t.Errorf("error %q should mention exceeds maximum length", err.Error())
	}
}

func TestRunAdd_UnknownFlag_SuggestsNearest(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	// --confidence is close to --provenance (audit finding mode 5/6)
	err := runAdd(context.Background(), app, []string{"--confidence", "verified", "some fact"}, "add")
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "--confidence") {
		t.Errorf("error %q should name the unknown flag", err.Error())
	}
	if !strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("error %q should suggest a valid flag", err.Error())
	}
}

func TestRunAdd_UnknownFlag_NoSuggestionForGarbage(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--zzzzgarbage", "some fact"}, "add")
	if err == nil {
		t.Fatal("expected error")
	}
	// No suggestion should be offered for an unrelated flag.
	if strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("error %q should not suggest a flag for garbage input", err.Error())
	}
	// But usage hint should still appear.
	if !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("error %q should include Usage: hint", err.Error())
	}
}

func TestRunAdd_OutputConfirmation_Fields(t *testing.T) {
	repo := &stubEntryRepo{}
	var out bytes.Buffer
	app := newTestApp(repo)
	app.Printer = NewPrinter(&out, false, false)

	err := runAdd(context.Background(), app, []string{"a stored fact"}, "add")
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Stored") {
		t.Errorf("output %q should contain 'Stored'", got)
	}
	if repo.created == nil {
		t.Fatal("expected entry")
	}
	if !strings.Contains(got, repo.created.ID.String()) {
		t.Errorf("output %q should contain the entry ULID %s", got, repo.created.ID)
	}
	if !strings.Contains(got, "root") { // default scope
		t.Errorf("output %q should contain scope", got)
	}
	if !strings.Contains(got, "a stored fact") {
		t.Errorf("output %q should echo content", got)
	}
}

func TestRunAdd_OutputJSON_Schema(t *testing.T) {
	repo := &stubEntryRepo{}
	var out bytes.Buffer
	app := newTestApp(repo)
	app.Printer = NewPrinter(&out, true, false)

	err := runAdd(context.Background(), app, []string{"json fact"}, "add")
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	got := out.String()
	// Must contain stable JSON fields.
	for _, field := range []string{`"id"`, `"scope"`, `"content"`, `"deduped"`, `"suggestions"`} {
		if !strings.Contains(got, field) {
			t.Errorf("JSON output %q missing field %s", got, field)
		}
	}
	// id must be a string value (ULID), not a number.
	if strings.Contains(got, `"id": [0-9]`) {
		t.Error("id should be a string (ULID), not a number")
	}
	if !strings.Contains(got, `"deduped": false`) {
		t.Errorf("deduped field should be false for new entry, got: %s", got)
	}
}

func TestRunAdd_Dedup_ShowsExistingID(t *testing.T) {
	// Simulate a dedup hit: CreateOrUpdate returns Version > 1.
	repo := &stubEntryRepo{
		versionToReturn: 2, // triggers deduped path
	}
	var out bytes.Buffer
	app := newTestApp(repo)
	app.Printer = NewPrinter(&out, false, false)

	err := runAdd(context.Background(), app, []string{"duplicate fact"}, "add")
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Duplicate") {
		t.Errorf("output %q should say Duplicate on dedup hit", got)
	}
	if !strings.Contains(got, repo.created.ID.String()) {
		t.Errorf("output %q should contain existing entry ID", got)
	}
	// Design requires next-action hints on dedup so the agent knows what to do.
	if !strings.Contains(got, "known update") {
		t.Errorf("output %q should include 'known update' hint", got)
	}
	if !strings.Contains(got, "elaborates:") {
		t.Errorf("output %q should include '--link elaborates:' hint", got)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"confidence", "provenance", 7},
		{"scope", "scope", 0},
		{"ttl", "ttl", 0},
		{"source", "source-type", 5},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, c := range cases {
		got := levenshtein(c.a, c.b)
		if got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNearestFlag_Confidence(t *testing.T) {
	// --confidence should suggest --provenance (closest real flag)
	got := nearestFlag("confidence", validAddFlags)
	if got == "" {
		t.Fatal("expected a suggestion for 'confidence'")
	}
	// provenance is the most semantically apt; score-wise accept any valid result
	t.Logf("nearest flag for 'confidence' = %q", got)
}

func TestNearestFlag_Garbage(t *testing.T) {
	got := nearestFlag("zzzzgarbage", validAddFlags)
	if got != "" {
		t.Errorf("expected no suggestion for 'zzzzgarbage', got %q", got)
	}
}

func TestNearestFlag_Source(t *testing.T) {
	// --source is the second audit-identified invented flag.
	// It ties on distance with "scope" but should resolve to "source-ref"
	// (or "source-type") via the prefix tie-break, not "scope".
	got := nearestFlag("source", validAddFlags)
	if got == "" {
		t.Fatal("expected a suggestion for 'source'")
	}
	if got == "scope" {
		t.Errorf("nearestFlag(\"source\") = %q; should prefer a source-* flag, not scope", got)
	}
	if !strings.HasPrefix(got, "source") {
		t.Errorf("nearestFlag(\"source\") = %q; want a source-* flag", got)
	}
}

// TestRunAdd_SourceFlag_Suggestion verifies the full error path for --source.
func TestRunAdd_SourceFlag_Suggestion(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--source", "conversation", "fact"}, "add")
	if err == nil {
		t.Fatal("expected error for unknown --source flag")
	}
	if !strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("error %q should suggest a valid flag", err.Error())
	}
	if strings.Contains(err.Error(), "--scope") {
		t.Errorf("error %q should not suggest --scope for --source", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Atomicity and --supersedes tests
// ---------------------------------------------------------------------------

// failOnNthEdgeRepo wraps a real EdgeRepo and returns an error on the Nth Create call.
// All other calls are delegated to the real repo.
type failOnNthEdgeRepo struct {
	real   storage.EdgeRepo
	failOn int
	calls  int
}

func (f *failOnNthEdgeRepo) Create(ctx context.Context, edge *model.Edge) error {
	f.calls++
	if f.calls == f.failOn {
		return errors.New("injected edge failure")
	}
	return f.real.Create(ctx, edge)
}
func (f *failOnNthEdgeRepo) Get(ctx context.Context, id model.ID) (*model.Edge, error) {
	return f.real.Get(ctx, id)
}
func (f *failOnNthEdgeRepo) Update(ctx context.Context, edge *model.Edge) error {
	return f.real.Update(ctx, edge)
}
func (f *failOnNthEdgeRepo) Delete(ctx context.Context, id model.ID) error {
	return f.real.Delete(ctx, id)
}
func (f *failOnNthEdgeRepo) EdgesFrom(ctx context.Context, id model.ID, filt storage.EdgeFilter) ([]model.Edge, error) {
	return f.real.EdgesFrom(ctx, id, filt)
}
func (f *failOnNthEdgeRepo) EdgesTo(ctx context.Context, id model.ID, filt storage.EdgeFilter) ([]model.Edge, error) {
	return f.real.EdgesTo(ctx, id, filt)
}
func (f *failOnNthEdgeRepo) EdgesBetween(ctx context.Context, a, b model.ID) ([]model.Edge, error) {
	return f.real.EdgesBetween(ctx, a, b)
}
func (f *failOnNthEdgeRepo) FindConflicts(ctx context.Context, id model.ID) ([]model.Entry, error) {
	return f.real.FindConflicts(ctx, id)
}

var _ storage.EdgeRepo = (*failOnNthEdgeRepo)(nil)

// newMigratedDB opens an in-memory SQLite backend and runs migrations.
func newMigratedDB(t *testing.T) storage.Backend {
	t.Helper()
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// makeTestEntry builds a test entry with an embedding.
func makeTestEntry(content string, vec []float32) model.Entry {
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e := model.NewEntry(content, src)
	e.Scope = model.RootScope
	return e.WithEmbedding(vec, "stub")
}

// TestRunAdd_Atomicity_SecondEdgeFails_NothingPersisted uses a real SQLite
// backend to verify that when the second edge creation fails inside WithTx,
// both the entry and all edges are rolled back.
func TestRunAdd_Atomicity_SecondEdgeFails_NothingPersisted(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	// Pre-seed two link targets.
	target1 := makeTestEntry("target one atomicity", []float32{0, 0, 1})
	if _, err := db.Entries().CreateOrUpdate(ctx, &target1); err != nil {
		t.Fatal(err)
	}
	target2 := makeTestEntry("target two atomicity", []float32{0, 1, 0})
	if _, err := db.Entries().CreateOrUpdate(ctx, &target2); err != nil {
		t.Fatal(err)
	}

	// failEdge fails on the 2nd Create call (second link) — the entire tx rolls back.
	failEdge := &failOnNthEdgeRepo{real: db.Edges(), failOn: 2}

	stub := &stubEmbedder{dims: 3}
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    failEdge,
		Embedder: stub,
		// No Engine: --link resolution uses raw ULIDs so searchSemanticScored is never called.
		Printer: NewPrinter(&bytes.Buffer{}, false, true),
		Stderr:  &bytes.Buffer{},
		Config:  &AppConfig{DefaultScope: model.RootScope, MaxContentLength: model.MaxContentLength},
	}

	err := runAdd(ctx, app, []string{
		"atomicity test entry content",
		"--link", fmt.Sprintf("elaborates:%s", target1.ID),
		"--link", fmt.Sprintf("related-to:%s", target2.ID),
	})
	if err == nil {
		t.Fatal("expected error from second-edge failure")
	}
	if !strings.Contains(err.Error(), "injected edge failure") {
		t.Errorf("error %q should mention injected failure", err.Error())
	}

	// The entry must NOT exist — it was rolled back by WithTx.
	entries, listErr := db.Entries().List(ctx, storage.EntryFilter{Scope: model.RootScope})
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, e := range entries {
		if e.Content == "atomicity test entry content" {
			t.Error("entry should have been rolled back but was found in the store")
		}
	}
}

// newSupersedingApp builds an App for --supersedes tests using a real SQLite
// backend and the stub embedder. Follows the pattern from link_test.go.
func newSupersedingApp(t *testing.T, db storage.Backend, out *bytes.Buffer) *App {
	t.Helper()
	stub := &stubEmbedder{dims: 3}
	return &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: stub,
		Engine:   query.New(db.Entries(), db.Edges(), stub),
		Printer:  NewPrinter(out, false, false),
		Stderr:   &bytes.Buffer{},
		Config:   &AppConfig{DefaultScope: model.RootScope, MaxContentLength: model.MaxContentLength},
	}
}

// TestRunAdd_Supersedes_HappyPath verifies that --supersedes creates a supersedes edge
// from the new entry to the resolved target, atomically, and prints confirmation.
func TestRunAdd_Supersedes_HappyPath(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	old := makeTestEntry("renderer architecture decision original", []float32{1, 0, 0})
	stored, err := db.Entries().CreateOrUpdate(ctx, &old)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	app := newSupersedingApp(t, db, &buf)

	// Use the exact content as the --supersedes query; the exact-match path fires.
	if err := runAdd(ctx, app, []string{
		"renderer architecture decision CORRECTION",
		"--supersedes", old.Content,
	}); err != nil {
		t.Fatalf("runAdd --supersedes: %v", err)
	}

	// Find the new entry.
	entries, _ := db.Entries().List(ctx, storage.EntryFilter{Scope: model.RootScope})
	var newEntry *model.Entry
	for i, e := range entries {
		if e.Content == "renderer architecture decision CORRECTION" {
			newEntry = &entries[i]
		}
	}
	if newEntry == nil {
		t.Fatal("new entry not found")
	}

	// Verify supersedes edge: new -[supersedes]-> old.
	edges, err := db.Edges().EdgesFrom(ctx, newEntry.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range edges {
		if e.Type == model.EdgeSupersedes && e.ToID == stored.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected supersedes edge new→old; edges from new: %+v", edges)
	}

	// Confirmation output must mention supersedes.
	if out := buf.String(); !strings.Contains(out, "Supersedes") {
		t.Errorf("output %q should contain 'Supersedes'", out)
	}
}

// TestRunAdd_Supersedes_Ambiguous verifies that an ambiguous --supersedes query
// aborts before writing anything (consistent with 7dw semantics).
func TestRunAdd_Supersedes_Ambiguous(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	// Store two entries that both match "renderer design" (neither is an exact match).
	for _, content := range []string{"renderer design alpha", "renderer design beta"} {
		e := makeTestEntry(content, []float32{1, 0, 0})
		if _, err := db.Entries().CreateOrUpdate(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	app := newSupersedingApp(t, db, &bytes.Buffer{})

	err := runAdd(ctx, app, []string{
		"new correction entry",
		"--supersedes", "renderer design",
	})
	if err == nil {
		t.Fatal("expected error for ambiguous --supersedes query")
	}
	if !strings.Contains(err.Error(), "--supersedes") {
		t.Errorf("error %q should mention --supersedes", err.Error())
	}

	// No new entry should have been written.
	entries, _ := db.Entries().List(ctx, storage.EntryFilter{Scope: model.RootScope})
	for _, e := range entries {
		if e.Content == "new correction entry" {
			t.Error("entry must not be persisted after ambiguous --supersedes")
		}
	}
}

// TestRunAdd_Supersedes_NoMatch verifies that a no-match --supersedes query
// aborts before writing anything.
func TestRunAdd_Supersedes_NoMatch(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	app := newSupersedingApp(t, db, &bytes.Buffer{})

	err := runAdd(ctx, app, []string{
		"new correction no match",
		"--supersedes", "nonexistent content xyz qam",
	})
	if err == nil {
		t.Fatal("expected error for no-match --supersedes query")
	}
	if !strings.Contains(err.Error(), "--supersedes") {
		t.Errorf("error %q should mention --supersedes", err.Error())
	}

	// No new entry written.
	entries, _ := db.Entries().List(ctx, storage.EntryFilter{Scope: model.RootScope})
	for _, e := range entries {
		if e.Content == "new correction no match" {
			t.Error("entry must not be persisted on no-match --supersedes")
		}
	}
}

// TestRunAdd_Supersedes_EdgeDirection verifies the direction of the supersedes edge:
// new_entry -[supersedes]-> old_entry, not the reverse.
func TestRunAdd_Supersedes_EdgeDirection(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	old := makeTestEntry("old fact to be superseded direction test", []float32{0, 1, 0})
	storedOld, err := db.Entries().CreateOrUpdate(ctx, &old)
	if err != nil {
		t.Fatal(err)
	}

	app := newSupersedingApp(t, db, &bytes.Buffer{})

	if err := runAdd(ctx, app, []string{
		"new fact superseding old direction test",
		"--supersedes", old.Content,
	}); err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	entries, _ := db.Entries().List(ctx, storage.EntryFilter{Scope: model.RootScope})
	var newEntry *model.Entry
	for i, e := range entries {
		if e.Content == "new fact superseding old direction test" {
			newEntry = &entries[i]
		}
	}
	if newEntry == nil {
		t.Fatal("new entry not found")
	}

	// Direction must be: new -[supersedes]-> old.
	edgesFrom, err := db.Edges().EdgesFrom(ctx, newEntry.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range edgesFrom {
		if e.Type == model.EdgeSupersedes && e.ToID == storedOld.ID && e.FromID == newEntry.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected new -[supersedes]-> old in EdgesFrom(new); got %+v", edgesFrom)
	}

	// Verify old does NOT have a supersedes edge FROM it to new (wrong direction).
	edgesFromOld, _ := db.Edges().EdgesFrom(ctx, storedOld.ID, storage.EdgeFilter{})
	for _, e := range edgesFromOld {
		if e.Type == model.EdgeSupersedes {
			t.Errorf("old entry should not have a supersedes edge FROM it; got %+v", e)
		}
	}
}

// TestRunAdd_Supersedes_SelfSupersede verifies that add 'X' --supersedes 'X'
// (where X already exists and dedup fires) aborts with a clear error and
// does not leave any partial state.
func TestRunAdd_Supersedes_SelfSupersede(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)

	existing := makeTestEntry("fact that already exists", []float32{1, 0, 0})
	if _, err := db.Entries().CreateOrUpdate(ctx, &existing); err != nil {
		t.Fatal(err)
	}

	app := newSupersedingApp(t, db, &bytes.Buffer{})

	// add 'fact that already exists' --supersedes 'fact that already exists':
	// CreateOrUpdate deduplicates and returns the existing entry ID, which
	// equals the resolved supersedesID → self-supersede guard fires.
	err := runAdd(ctx, app, []string{
		existing.Content,
		"--supersedes", existing.Content,
	})
	if err == nil {
		t.Fatal("expected error for self-supersede")
	}
	if !strings.Contains(err.Error(), "cannot supersede itself") {
		t.Errorf("error %q should say 'cannot supersede itself'", err.Error())
	}

	// No new edges should have been created.
	edges, _ := db.Edges().EdgesFrom(ctx, existing.ID, storage.EdgeFilter{})
	for _, e := range edges {
		if e.Type == model.EdgeSupersedes {
			t.Errorf("no supersedes edge should exist after self-supersede refusal; got %+v", e)
		}
	}
}
