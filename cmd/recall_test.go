package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// ---------------------------------------------------------------------------
// Helpers — reuse stubs from add_test.go where possible, add recall-specific
// stubs for the query engine and entry listing.
// ---------------------------------------------------------------------------

// recallEntryRepo extends stubEntryRepo with List support for recallByScope.
type recallEntryRepo struct {
	stubEntryRepo
	listEntries []model.Entry
	listFilter  storage.EntryFilter // captured for inspection
}

func (r *recallEntryRepo) List(_ context.Context, filter storage.EntryFilter) ([]model.Entry, error) {
	r.listFilter = filter
	return r.listEntries, nil
}

// newRecallTestApp constructs a minimal App for recall tests.
// The Engine is constructed with the given repos and a stub embedder so that
// SearchHybrid can be called (though it will return zero results with a zero
// vector since the stub embedder produces zero embeddings).
func newRecallTestApp(repo *recallEntryRepo) *App {
	edgeRepo := &stubEdgeRepo{}
	embedder := &stubEmbedder{dims: 3}
	return &App{
		Entries:  repo,
		Edges:    edgeRepo,
		Embedder: embedder,
		Engine:   query.New(repo, edgeRepo, embedder),
		Printer:  NewPrinter(&bytes.Buffer{}, false, true),
		Config: &AppConfig{
			DefaultScope: "root",
		},
	}
}

// ---------------------------------------------------------------------------
// Tests — flag defaults and wiring
// ---------------------------------------------------------------------------

func TestRunRecall_DefaultFlags_PreserveExistingBehavior(t *testing.T) {
	// When called with only a query, the defaults should match the
	// previously-hardcoded values: threshold=0.3, recency=0.1, expand-depth=1.
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	// runRecall will call SearchHybrid which calls SearchVector.
	// With a zero-vector embedding and empty DB, we expect zero results — no error.
	err := runRecall(context.Background(), app, []string{"test query"})
	if err != nil {
		t.Fatalf("runRecall with defaults: %v", err)
	}
}

func TestRunRecall_ThresholdFlag(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--threshold", "0.5", "test query"})
	if err != nil {
		t.Fatalf("runRecall with --threshold 0.5: %v", err)
	}
}

func TestRunRecall_RecencyFlag(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--recency", "0.5", "test query"})
	if err != nil {
		t.Fatalf("runRecall with --recency 0.5: %v", err)
	}
}

func TestRunRecall_ExpandDepthFlag(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--expand-depth", "3", "test query"})
	if err != nil {
		t.Fatalf("runRecall with --expand-depth 3: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests — provenance validation
// ---------------------------------------------------------------------------

func TestRunRecall_ProvenanceValid(t *testing.T) {
	for _, level := range []string{"verified", "inferred", "uncertain"} {
		t.Run(level, func(t *testing.T) {
			repo := &recallEntryRepo{}
			app := newRecallTestApp(repo)
			err := runRecall(context.Background(), app, []string{"--provenance", level, "test query"})
			if err != nil {
				t.Fatalf("runRecall with --provenance %s: %v", level, err)
			}
		})
	}
}

func TestRunRecall_ProvenanceInvalid(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--provenance", "bogus", "test query"})
	if err == nil {
		t.Fatal("expected error for invalid --provenance")
	}
	if !contains(err.Error(), "invalid --provenance") {
		t.Errorf("error %q should contain 'invalid --provenance'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Tests — source validation
// ---------------------------------------------------------------------------

func TestRunRecall_SourceValid(t *testing.T) {
	for _, src := range []string{"file", "url", "conversation", "manual"} {
		t.Run(src, func(t *testing.T) {
			repo := &recallEntryRepo{}
			app := newRecallTestApp(repo)
			err := runRecall(context.Background(), app, []string{"--source", src, "test query"})
			if err != nil {
				t.Fatalf("runRecall with --source %s: %v", src, err)
			}
		})
	}
}

func TestRunRecall_SourceInvalid(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--source", "bogus", "test query"})
	if err == nil {
		t.Fatal("expected error for invalid --source")
	}
	if !contains(err.Error(), "invalid --source") {
		t.Errorf("error %q should contain 'invalid --source'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Tests — recallByScope with new filters
// ---------------------------------------------------------------------------

func TestRunRecall_ScopeMode_PassesFilters(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{
		"--scope", "test",
		"--provenance", "verified",
		"--source", "file",
	})
	if err != nil {
		t.Fatalf("runRecall scope mode: %v", err)
	}

	// Verify the filter was passed to List.
	if repo.listFilter.ProvenanceLevel != model.ProvenanceVerified {
		t.Errorf("ProvenanceLevel = %q, want %q", repo.listFilter.ProvenanceLevel, model.ProvenanceVerified)
	}
	if repo.listFilter.SourceType != model.SourceFile {
		t.Errorf("SourceType = %q, want %q", repo.listFilter.SourceType, model.SourceFile)
	}
}

func TestRunRecall_ScopeMode_DefaultsPreserved(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{"--scope", "test"})
	if err != nil {
		t.Fatalf("runRecall scope mode defaults: %v", err)
	}

	// Without --provenance and --source, filter fields should be empty (no filtering).
	if repo.listFilter.ProvenanceLevel != "" {
		t.Errorf("ProvenanceLevel should be empty, got %q", repo.listFilter.ProvenanceLevel)
	}
	if repo.listFilter.SourceType != "" {
		t.Errorf("SourceType should be empty, got %q", repo.listFilter.SourceType)
	}
}

// ---------------------------------------------------------------------------
// Tests — post-filter functions
// ---------------------------------------------------------------------------

func TestFilterResultsByProvenance(t *testing.T) {
	results := []query.Result{
		{Entry: model.Entry{Provenance: model.Provenance{Level: model.ProvenanceVerified}}},
		{Entry: model.Entry{Provenance: model.Provenance{Level: model.ProvenanceInferred}}},
		{Entry: model.Entry{Provenance: model.Provenance{Level: model.ProvenanceUncertain}}},
	}

	t.Run("empty level returns all", func(t *testing.T) {
		got := filterResultsByProvenance(results, "")
		if len(got) != 3 {
			t.Errorf("expected 3 results, got %d", len(got))
		}
	})

	t.Run("filters to verified only", func(t *testing.T) {
		got := filterResultsByProvenance(results, model.ProvenanceVerified)
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0].Entry.Provenance.Level != model.ProvenanceVerified {
			t.Errorf("expected verified, got %s", got[0].Entry.Provenance.Level)
		}
	})

	t.Run("filters to inferred only", func(t *testing.T) {
		got := filterResultsByProvenance(results, model.ProvenanceInferred)
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0].Entry.Provenance.Level != model.ProvenanceInferred {
			t.Errorf("expected inferred, got %s", got[0].Entry.Provenance.Level)
		}
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		single := []query.Result{
			{Entry: model.Entry{Provenance: model.Provenance{Level: model.ProvenanceVerified}}},
		}
		got := filterResultsByProvenance(single, model.ProvenanceUncertain)
		if len(got) != 0 {
			t.Errorf("expected 0 results, got %d", len(got))
		}
	})
}

func TestFilterResultsBySource(t *testing.T) {
	now := time.Now()
	results := []query.Result{
		{Entry: model.Entry{
			Source:    model.Source{Type: model.SourceFile, Reference: "test.go"},
			CreatedAt: now,
		}},
		{Entry: model.Entry{
			Source:    model.Source{Type: model.SourceConversation, Reference: "chat"},
			CreatedAt: now,
		}},
		{Entry: model.Entry{
			Source:    model.Source{Type: model.SourceManual, Reference: "cli"},
			CreatedAt: now,
		}},
	}

	t.Run("empty source returns all", func(t *testing.T) {
		got := filterResultsBySource(results, "")
		if len(got) != 3 {
			t.Errorf("expected 3 results, got %d", len(got))
		}
	})

	t.Run("filters to file only", func(t *testing.T) {
		got := filterResultsBySource(results, model.SourceFile)
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0].Entry.Source.Type != model.SourceFile {
			t.Errorf("expected file, got %s", got[0].Entry.Source.Type)
		}
	})

	t.Run("filters to conversation only", func(t *testing.T) {
		got := filterResultsBySource(results, model.SourceConversation)
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0].Entry.Source.Type != model.SourceConversation {
			t.Errorf("expected conversation, got %s", got[0].Entry.Source.Type)
		}
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		got := filterResultsBySource(results, model.SourceURL)
		if len(got) != 0 {
			t.Errorf("expected 0 results, got %d", len(got))
		}
	})
}

// ---------------------------------------------------------------------------
// Tests — combined filters
// ---------------------------------------------------------------------------

func TestRunRecall_CombinedProvenanceAndSource(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	// Both provenance and source filters should be accepted together.
	err := runRecall(context.Background(), app, []string{
		"--provenance", "verified",
		"--source", "conversation",
		"test query",
	})
	if err != nil {
		t.Fatalf("runRecall with combined filters: %v", err)
	}
}

func TestRunRecall_AllFlagsTogether(t *testing.T) {
	repo := &recallEntryRepo{}
	app := newRecallTestApp(repo)

	err := runRecall(context.Background(), app, []string{
		"--threshold", "0.5",
		"--recency", "0.2",
		"--expand-depth", "2",
		"--provenance", "inferred",
		"--source", "manual",
		"--label", "lang:go",
		"test query",
	})
	if err != nil {
		t.Fatalf("runRecall with all flags: %v", err)
	}
}

// Verify the recallEntryRepo satisfies the interface.
var _ storage.EntryRepo = (*recallEntryRepo)(nil)
