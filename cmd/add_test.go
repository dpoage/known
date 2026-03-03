package cmd

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// ---------------------------------------------------------------------------
// Minimal mocks for runAdd tests
// ---------------------------------------------------------------------------

// stubEntryRepo captures the entry passed to CreateOrUpdate so tests can
// inspect it. Only the methods used by runAdd are implemented.
// Pre-seed entries in the `existing` map for Get lookups (used by --link).
type stubEntryRepo struct {
	created  *model.Entry
	existing map[model.ID]*model.Entry
}

func (s *stubEntryRepo) CreateOrUpdate(_ context.Context, entry *model.Entry) (*model.Entry, error) {
	clone := *entry
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
func (s *stubEdgeRepo) Delete(context.Context, model.ID) error   { return nil }
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
func (e *stubEmbedder) Dimensions() int  { return e.dims }
func (e *stubEmbedder) ModelName() string { return "stub" }

// newTestApp constructs a minimal App suitable for calling runAdd.
func newTestApp(repo *stubEntryRepo) *App {
	return &App{
		Entries:  repo,
		Embedder: &stubEmbedder{dims: 3},
		Printer:  NewPrinter(&bytes.Buffer{}, false, true),
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRunAdd_TTLZero_PermanentEntry(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--ttl", "0", "permanent fact"})
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

func TestRunAdd_TTLZero_OverridesDefault(t *testing.T) {
	// Ensure --ttl 0 prevents the default TTL from being applied,
	// even for source types that have a default.
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{
		"--ttl", "0",
		"--source-type", "conversation",
		"override default TTL",
	})
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}
	if repo.created.TTL != nil {
		t.Errorf("TTL should be nil (--ttl 0 overrides conversation default), got %v", repo.created.TTL)
	}
	if repo.created.ExpiresAt != nil {
		t.Errorf("ExpiresAt should be nil, got %v", repo.created.ExpiresAt)
	}
}

func TestRunAdd_TTLExplicit_SetsExpiry(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--ttl", "24h", "ephemeral fact"})
	if err != nil {
		t.Fatalf("runAdd with --ttl 24h: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}
	if repo.created.TTL == nil {
		t.Fatal("TTL should be set for --ttl 24h")
	}
	if repo.created.TTL.Duration != 24*time.Hour {
		t.Errorf("TTL = %v, want 24h", repo.created.TTL.Duration)
	}
	if repo.created.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set for --ttl 24h")
	}
	// ExpiresAt should be approximately CreatedAt + 24h.
	diff := repo.created.ExpiresAt.Sub(repo.created.CreatedAt)
	if math.Abs(diff.Hours()-24) > 0.01 {
		t.Errorf("ExpiresAt - CreatedAt = %v, want ~24h", diff)
	}
}

func TestRunAdd_NoTTLFlag_AppliesDefault(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	// source-type=manual has a 90d default TTL in newTestApp.
	err := runAdd(context.Background(), app, []string{"--source-type", "manual", "fact with default ttl"})
	if err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected an entry to be created")
	}
	if repo.created.TTL == nil {
		t.Fatal("TTL should be set from default")
	}
	want := 90 * 24 * time.Hour
	if repo.created.TTL.Duration != want {
		t.Errorf("TTL = %v, want %v", repo.created.TTL.Duration, want)
	}
	if repo.created.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set from default TTL")
	}
}

func TestRunAdd_InvalidTTL(t *testing.T) {
	repo := &stubEntryRepo{}
	app := newTestApp(repo)

	err := runAdd(context.Background(), app, []string{"--ttl", "notaduration", "bad ttl"})
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

	err := runAdd(context.Background(), app, []string{})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
	if !strings.Contains(err.Error(), "content argument is required") {
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
	})
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
	})
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
	})
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
	})
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
