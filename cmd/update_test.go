package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// stubUpdateRepo supports Get (returns a seeded entry) and Update (captures result).
type stubUpdateRepo struct {
	seed    *model.Entry
	updated *model.Entry
}

func (s *stubUpdateRepo) Get(_ context.Context, id model.ID) (*model.Entry, error) {
	if s.seed != nil && s.seed.ID == id {
		clone := *s.seed
		return &clone, nil
	}
	return nil, storage.ErrNotFound
}
func (s *stubUpdateRepo) Update(_ context.Context, entry *model.Entry) error {
	clone := *entry
	s.updated = &clone
	return nil
}
func (s *stubUpdateRepo) Create(context.Context, *model.Entry) error { return nil }
func (s *stubUpdateRepo) CreateOrUpdate(_ context.Context, entry *model.Entry) (*model.Entry, error) {
	clone := *entry
	s.updated = &clone
	return &clone, nil
}
func (s *stubUpdateRepo) Delete(context.Context, model.ID) error { return nil }
func (s *stubUpdateRepo) List(context.Context, storage.EntryFilter) ([]model.Entry, error) {
	return nil, nil
}
func (s *stubUpdateRepo) SearchSimilar(context.Context, []float32, string, storage.SimilarityMetric, int) ([]storage.SimilarityResult, error) {
	return nil, nil
}
func (s *stubUpdateRepo) DeleteExpired(context.Context) (int64, error) { return 0, nil }
func (s *stubUpdateRepo) SearchText(context.Context, string, string, int) ([]storage.SimilarityResult, error) {
	return nil, nil
}

// TestRunUpdate_AdvancesObservedAt verifies that known-update sets
// Freshness.ObservedAt=now, making the updated entry appear fresh to
// freshnessScoreAt regardless of the original CreatedAt.
// This is the oj3 acceptance: update IS re-verification.
func TestRunUpdate_AdvancesObservedAt(t *testing.T) {
	ctx := context.Background()
	src := model.Source{Type: model.SourceManual, Reference: "test"}

	// Entry created and last observed one year ago.
	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	e := model.NewEntry("the gateway IP is 10.0.1.5", src).WithScope("root")
	e.CreatedAt = oneYearAgo
	e.Freshness.ObservedAt = oneYearAgo

	repo := &stubUpdateRepo{seed: &e}
	app := &App{
		Entries:  repo,
		Embedder: &stubEmbedder{dims: 3},
		Printer:  NewPrinter(&bytes.Buffer{}, false, true),
		Config: &AppConfig{
			DefaultScope:     "root",
			MaxContentLength: model.MaxContentLength,
		},
	}

	before := time.Now()
	err := runUpdate(ctx, app, []string{e.ID.String(), "--content", "the gateway IP is 10.0.1.5 (verified)"})
	if err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	if repo.updated == nil {
		t.Fatal("expected entry to be updated")
	}

	obs := repo.updated.Freshness.ObservedAt
	if obs.Before(before) {
		t.Errorf("ObservedAt = %v, want >= %v (update must advance re-verification time)", obs, before)
	}
	if obs.IsZero() {
		t.Error("ObservedAt is zero after update — re-verification timestamp not set")
	}
	// CreatedAt must be unchanged.
	if !repo.updated.CreatedAt.Equal(oneYearAgo) {
		t.Errorf("CreatedAt changed from %v to %v — update must not change creation time", oneYearAgo, repo.updated.CreatedAt)
	}
}
