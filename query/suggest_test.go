package query

import (
	"context"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// unit vectors for deterministic cosine distances (reuse makeEntry from query_test.go).
var (
	sugVecA = []float32{1, 0, 0, 0}            // entry A direction
	sugVecB = []float32{0, 1, 0, 0}            // orthogonal to A
	sugVecC = []float32{0.9, 0.436, 0, 0}      // close to A
	sugVecD = []float32{0, 0, 1, 0}            // unrelated
)

func TestSuggestLinks_TopK(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	eB := makeEntry("entry B", model.RootScope, sugVecB)
	eC := makeEntry("entry C — similar to A", model.RootScope, sugVecC)
	eD := makeEntry("entry D — unrelated", model.RootScope, sugVecD)
	for _, e := range []model.Entry{eB, eC, eD} {
		e := e
		if err := entryRepo.Create(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	eng := New(entryRepo, edgeRepo, nil) // embedder not needed for SuggestLinks

	eA := makeEntry("entry A", model.RootScope, sugVecA)

	suggestions, err := eng.SuggestLinks(ctx, eA, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(suggestions))
	}

	// C is closest to A (smallest cosine distance), should appear first.
	if suggestions[0].Entry.ID != eC.ID {
		t.Errorf("expected closest neighbor eC, got content=%q", suggestions[0].Entry.Content)
	}

	for _, s := range suggestions {
		if s.EdgeType != model.EdgeRelatedTo {
			t.Errorf("expected related-to edge type, got %s", s.EdgeType)
		}
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("score out of [0,1]: %f", s.Score)
		}
	}
}

func TestSuggestLinks_ExcludesSelf(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	eng := New(entryRepo, edgeRepo, nil)

	// Only the source entry is in the store — should return no suggestions.
	eA := makeEntry("sole entry", model.RootScope, sugVecA)
	stored := eA
	if err := entryRepo.Create(ctx, &stored); err != nil {
		t.Fatal(err)
	}

	suggestions, err := eng.SuggestLinks(ctx, eA, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range suggestions {
		if s.Entry.ID == eA.ID {
			t.Errorf("SuggestLinks returned self as suggestion")
		}
	}
}

func TestSuggestLinks_NoEmbedding_TextFallback(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	// Inject a fake SearchText result.
	fakeMatch := makeEntry("matching text entry", model.RootScope, nil)
	entryRepo.searchText = func(_ context.Context, query, scope string, limit int) ([]storage.SimilarityResult, error) {
		return []storage.SimilarityResult{{Entry: fakeMatch, Distance: -1.5}}, nil
	}

	eng := New(entryRepo, edgeRepo, nil)

	// Entry without embedding.
	src := model.Source{Type: model.SourceManual}
	noEmbed := model.NewEntry("some content", src)
	noEmbed.Scope = model.RootScope

	suggestions, err := eng.SuggestLinks(ctx, noEmbed, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 text-fallback suggestion, got %d", len(suggestions))
	}
	if suggestions[0].EdgeType != model.EdgeRelatedTo {
		t.Errorf("expected related-to, got %s", suggestions[0].EdgeType)
	}
}

func TestSuggestLinks_EmptyStore(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	eng := New(entryRepo, edgeRepo, nil)

	eA := makeEntry("lonely entry", model.RootScope, sugVecA)
	suggestions, err := eng.SuggestLinks(ctx, eA, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions from empty store, got %d", len(suggestions))
	}
}

func TestSuggestLinks_KZeroDefaultsToThree(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	for i := 0; i < 5; i++ {
		v := make([]float32, 4)
		v[i%4] = 1
		e := makeEntry("neighbor", model.RootScope, v)
		if err := entryRepo.Create(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	eng := New(entryRepo, edgeRepo, nil)
	eA := makeEntry("source", model.RootScope, sugVecA)

	suggestions, err := eng.SuggestLinks(ctx, eA, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) > 3 {
		t.Errorf("k=0 should default to 3, got %d suggestions", len(suggestions))
	}
}
