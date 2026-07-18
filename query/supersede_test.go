package query

import (
	"context"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// makeSupersededEntry creates a model.Entry with a non-zero embedding.
// The slot parameter offsets which dimension is set to 1.0 so that two
// entries have different vectors and thus different cosine-similarity scores
// against the same query vector.
func makeSupersededEntry(content string, dim int, slot int) model.Entry {
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e := model.NewEntry(content, src).WithScope("root")
	e.Embedding = make([]float32, dim)
	e.EmbeddingDim = dim
	e.Embedding[slot%dim] = 1.0
	return e
}

func TestEnrichSuperseded_Demotion(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	const dim = 4
	embedder := newMockEmbedder(dim)

	original := makeSupersededEntry("original architecture decision renderer", dim, 0)
	correction := makeSupersededEntry("correction architecture decision renderer fix", dim, 1)

	if err := entryRepo.Create(ctx, &original); err != nil {
		t.Fatal(err)
	}
	if err := entryRepo.Create(ctx, &correction); err != nil {
		t.Fatal(err)
	}

	// correction supersedes original: edge from correction → original.
	edge := model.NewEdge(correction.ID, original.ID, model.EdgeSupersedes)
	if err := edgeRepo.Create(ctx, &edge); err != nil {
		t.Fatal(err)
	}

	eng := New(entryRepo, edgeRepo, embedder)

	opts := HybridOptions{
		Vector: VectorOptions{
			Text:  "architecture decision renderer",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:       1,
		ExpandDirection:   Both,
		TextSearch:        false,
		TotalLimit:        10,
		IncludeSuperseded: false,
	}

	results, err := eng.SearchHybrid(ctx, opts)
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Find the original (superseded) and correction results.
	var origResult, corrResult *Result
	for i := range results {
		if results[i].Entry.ID == original.ID {
			origResult = &results[i]
		}
		if results[i].Entry.ID == correction.ID {
			corrResult = &results[i]
		}
	}
	if origResult == nil {
		t.Fatal("original entry not in results")
	}
	if corrResult == nil {
		t.Fatal("correction entry not in results")
	}

	// Original must be marked superseded.
	if !origResult.IsSuperseded {
		t.Error("original entry: IsSuperseded must be true")
	}
	if len(origResult.SupersededBy) == 0 || origResult.SupersededBy[0] != correction.ID {
		t.Errorf("original entry: SupersededBy must contain correction ID, got %v", origResult.SupersededBy)
	}

	// Correction must rank above original (demotion pushed original's score down).
	if corrResult.Score <= origResult.Score {
		t.Errorf("correction score (%f) must be > original score (%f) after demotion",
			corrResult.Score, origResult.Score)
	}

	// First result must be the correction (not the superseded original).
	if results[0].Entry.ID != correction.ID {
		t.Errorf("first result must be correction, got %s (content: %s)",
			results[0].Entry.ID, results[0].Entry.Content)
	}
}

func TestEnrichSuperseded_IncludeSuperseded_NoChange(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	const dim = 4
	embedder := newMockEmbedder(dim)

	original := makeSupersededEntry("original entry nondemotion test", dim, 0)
	correction := makeSupersededEntry("correction entry nondemotion test fix", dim, 1)

	if err := entryRepo.Create(ctx, &original); err != nil {
		t.Fatal(err)
	}
	if err := entryRepo.Create(ctx, &correction); err != nil {
		t.Fatal(err)
	}

	edge := model.NewEdge(correction.ID, original.ID, model.EdgeSupersedes)
	if err := edgeRepo.Create(ctx, &edge); err != nil {
		t.Fatal(err)
	}

	eng := New(entryRepo, edgeRepo, embedder)

	opts := HybridOptions{
		Vector: VectorOptions{
			Text:  "entry nondemotion test",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:       1,
		ExpandDirection:   Both,
		TextSearch:        false,
		TotalLimit:        10,
		IncludeSuperseded: true, // no demotion
	}

	results, err := eng.SearchHybrid(ctx, opts)
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	// Find original result.
	var origResult *Result
	for i := range results {
		if results[i].Entry.ID == original.ID {
			origResult = &results[i]
			break
		}
	}
	if origResult == nil {
		t.Fatal("original entry not in results")
	}

	// IsSuperseded must be populated even when demotion is disabled.
	if !origResult.IsSuperseded {
		t.Error("original entry: IsSuperseded must be true even with IncludeSuperseded=true")
	}

	// Score must NOT be demoted — should remain above 0.01 for any realistic vector.
	if origResult.Score < 0.01 {
		t.Errorf("original entry score unexpectedly low (%f) with IncludeSuperseded=true — was it demoted?",
			origResult.Score)
	}
}

func TestLowRelevanceThreshold_Constant(t *testing.T) {
	if LowRelevanceThreshold != 0.3 {
		t.Errorf("LowRelevanceThreshold must be 0.3, got %f", LowRelevanceThreshold)
	}
}

func TestEnrichSuperseded_NoneSuperseded(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	const dim = 4
	embedder := newMockEmbedder(dim)

	e1 := makeSupersededEntry("standalone entry no supersede", dim, 0)
	if err := entryRepo.Create(ctx, &e1); err != nil {
		t.Fatal(err)
	}

	eng := New(entryRepo, edgeRepo, embedder)
	opts := HybridOptions{
		Vector: VectorOptions{
			Text:  "standalone entry no supersede",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Both,
		TotalLimit:      10,
	}

	results, err := eng.SearchHybrid(ctx, opts)
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}
	for _, r := range results {
		if r.IsSuperseded {
			t.Errorf("entry %s: IsSuperseded must be false when no supersedes edge exists", r.Entry.ID)
		}
	}
}

func TestEdgesTo_SupersedesFilter(t *testing.T) {
	// Verify the mockEdgeRepo EdgesTo with Type filter — the primitive enrichSuperseded depends on.
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)

	id1 := model.NewID()
	id2 := model.NewID()

	supersedes := model.NewEdge(id1, id2, model.EdgeSupersedes)
	related := model.NewEdge(id1, id2, model.EdgeRelatedTo)

	_ = edgeRepo.Create(ctx, &supersedes)
	_ = edgeRepo.Create(ctx, &related)

	edges, err := edgeRepo.EdgesTo(ctx, id2, storage.EdgeFilter{Type: model.EdgeSupersedes})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Type != model.EdgeSupersedes {
		t.Errorf("expected 1 supersedes edge, got %d", len(edges))
	}
}
