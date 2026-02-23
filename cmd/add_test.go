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
type stubEntryRepo struct {
	created *model.Entry
}

func (s *stubEntryRepo) CreateOrUpdate(_ context.Context, entry *model.Entry) (*model.Entry, error) {
	clone := *entry
	s.created = &clone
	return &clone, nil
}
func (s *stubEntryRepo) Create(context.Context, *model.Entry) error                    { return nil }
func (s *stubEntryRepo) Get(context.Context, model.ID) (*model.Entry, error)           { return nil, storage.ErrNotFound }
func (s *stubEntryRepo) Update(context.Context, *model.Entry) error                    { return nil }
func (s *stubEntryRepo) Delete(context.Context, model.ID) error                        { return nil }
func (s *stubEntryRepo) List(context.Context, storage.EntryFilter) ([]model.Entry, error) {
	return nil, nil
}
func (s *stubEntryRepo) SearchSimilar(context.Context, []float32, string, storage.SimilarityMetric, int) ([]storage.SimilarityResult, error) {
	return nil, nil
}
func (s *stubEntryRepo) DeleteExpired(context.Context) (int64, error) { return 0, nil }

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

// Verify interface compliance of stubs at compile time.
var _ storage.EntryRepo = (*stubEntryRepo)(nil)
