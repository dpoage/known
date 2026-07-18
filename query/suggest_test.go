package query

import (
	"context"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// unit vectors for deterministic cosine distances (reuse makeEntry from query_test.go).
var (
	sugVecA = []float32{1, 0, 0, 0}       // entry A direction
	sugVecB = []float32{0, 1, 0, 0}       // orthogonal to A
	sugVecC = []float32{0.9, 0.436, 0, 0} // close to A
	sugVecD = []float32{0, 0, 1, 0}       // unrelated
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

func TestSuggestLinks_CrossScope(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	eng := New(entryRepo, edgeRepo, nil)

	eA := makeEntry("source in project A", "projectA", sugVecA)

	// Same-scope neighbor: orthogonal (farthest).
	eSameFar := makeEntry("same-scope unrelated", "projectA", sugVecD)
	// Cross-scope neighbor: much closer to A than the same-scope entry.
	eCrossClose := makeEntry("cross-scope similar", "projectB", sugVecC)

	for _, e := range []model.Entry{eSameFar, eCrossClose} {
		e := e
		if err := entryRepo.Create(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	suggestions, err := eng.SuggestLinks(ctx, eA, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(suggestions))
	}

	// The higher-similarity neighbor must surface first even though it
	// lives in a different scope — this is the cross-project bug fix.
	if suggestions[0].Entry.ID != eCrossClose.ID {
		t.Errorf("expected cross-scope neighbor ranked first by similarity, got %q (scope=%s)",
			suggestions[0].Entry.Content, suggestions[0].Entry.Scope)
	}
	if suggestions[0].Entry.Scope == eA.Scope {
		t.Errorf("expected top suggestion from a different scope than %q, got %q",
			eA.Scope, suggestions[0].Entry.Scope)
	}
}

func TestSuggestLinks_ExcludesLinked_CrossScope(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	eng := New(entryRepo, edgeRepo, nil)

	eA := makeEntry("source", "projectA", sugVecA)
	if err := entryRepo.Create(ctx, &eA); err != nil {
		t.Fatal(err)
	}

	eCross := makeEntry("cross-scope neighbor", "projectB", sugVecC)
	if err := entryRepo.Create(ctx, &eCross); err != nil {
		t.Fatal(err)
	}

	// Already linked, in a different scope from the source.
	edge := model.NewEdge(eA.ID, eCross.ID, model.EdgeRelatedTo)
	if err := edgeRepo.Create(ctx, &edge); err != nil {
		t.Fatal(err)
	}

	suggestions, err := eng.SuggestLinks(ctx, eA, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range suggestions {
		if s.Entry.ID == eCross.ID {
			t.Errorf("already-linked cross-scope entry should stay excluded, got it in suggestions")
		}
	}
}

// TestSuggestLinks_TieOrdering_SameScopeThenID verifies the deterministic
// tie-break: entries at an identical similarity score sort same-scope-first,
// then by ID ascending. Storage-layer ordering for exact ties is otherwise
// unspecified (the mock iterates a Go map), so this test only passes if
// SuggestLinks applies its own stable tie-break sort.
func TestSuggestLinks_TieOrdering_SameScopeThenID(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	eng := New(entryRepo, edgeRepo, nil)

	eA := makeEntry("source entry", "root", sugVecA)

	// All three candidates share eA's exact embedding, so all three tie at
	// the maximum score (distance 0).
	eOther := makeEntry("cross-scope tie", "otherproj", sugVecA)
	eSame1 := makeEntry("same-scope tie one", "root", sugVecA)
	eSame2 := makeEntry("same-scope tie two", "root", sugVecA)

	for _, e := range []model.Entry{eOther, eSame1, eSame2} {
		e := e
		if err := entryRepo.Create(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	suggestions, err := eng.SuggestLinks(ctx, eA, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 3 {
		t.Fatalf("expected 3 suggestions, got %d", len(suggestions))
	}

	// The cross-scope tie must sort after both same-scope ties.
	if suggestions[2].Entry.ID != eOther.ID {
		t.Errorf("expected cross-scope tie last; got order %s, %s, %s",
			suggestions[0].Entry.ID, suggestions[1].Entry.ID, suggestions[2].Entry.ID)
	}

	// Among the same-scope ties, ID ascending breaks the tie.
	wantFirst, wantSecond := eSame1.ID, eSame2.ID
	if wantFirst.String() > wantSecond.String() {
		wantFirst, wantSecond = wantSecond, wantFirst
	}
	if suggestions[0].Entry.ID != wantFirst || suggestions[1].Entry.ID != wantSecond {
		t.Errorf("expected same-scope ties ordered by ID ascending (%s, %s); got (%s, %s)",
			wantFirst, wantSecond, suggestions[0].Entry.ID, suggestions[1].Entry.ID)
	}

	// Run repeatedly to guard against nondeterministic map-iteration order
	// leaking through despite the explicit sort.
	for i := range 10 {
		again, err := eng.SuggestLinks(ctx, eA, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for j := range again {
			if again[j].Entry.ID != suggestions[j].Entry.ID {
				t.Fatalf("nondeterministic ordering on repeat call %d: position %d got %s, want %s",
					i, j, again[j].Entry.ID, suggestions[j].Entry.ID)
			}
		}
	}
}
