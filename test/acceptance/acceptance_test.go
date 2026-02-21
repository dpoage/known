//go:build integration

// Package acceptance contains end-to-end acceptance tests that exercise
// multi-step workflows through the storage.Backend interface using an
// in-memory SQLite database. Each test gets its own isolated DB instance.
package acceptance

import (
	"context"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/sqlite"
	"github.com/stretchr/testify/require"
)

// newDB creates a fresh in-memory SQLite backend with all migrations applied.
// Each call returns a fully independent database instance.
func newDB(t *testing.T) storage.Backend {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.New(ctx, ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	require.NoError(t, db.Migrate(), "run migrations")
	t.Cleanup(func() { db.Close() })
	return db
}

// testSource returns a minimal valid Source for use in test entries.
func testSource(ref string) model.Source {
	return model.Source{
		Type:      model.SourceManual,
		Reference: ref,
	}
}

// ensureScope upserts a scope and fails the test if the operation fails.
func ensureScope(t *testing.T, db storage.Backend, path string) {
	t.Helper()
	ctx := context.Background()
	scope := model.NewScope(path)
	require.NoError(t, db.Scopes().Upsert(ctx, &scope), "upsert scope %q", path)
}

// =============================================================================
// TestAcceptance_AddAndSearch
//
// Adds 5 entries with distinct embeddings to the same scope, then issues a
// similarity search whose query vector is closest to exactly one entry. Verifies
// the top result is that entry and that its distance is below the cosine
// threshold (0.1 means nearly identical direction).
// =============================================================================

func TestAcceptance_AddAndSearch(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "search-test")

	src := testSource("acceptance-test")

	// Five entries. Embeddings are orthogonal or near-orthogonal 4-d vectors so
	// the ordering is deterministic regardless of normalization quirks.
	entries := []struct {
		content   string
		embedding []float32
	}{
		{"The sky is blue", []float32{1.0, 0.0, 0.0, 0.0}},
		{"Grass is green", []float32{0.0, 1.0, 0.0, 0.0}},
		{"Fire is hot", []float32{0.0, 0.0, 1.0, 0.0}},
		{"Water is cold", []float32{0.0, 0.0, 0.0, 1.0}},
		{"Snow is white", []float32{0.5, 0.5, 0.0, 0.0}},
	}

	for _, e := range entries {
		entry := model.NewEntry(e.content, src).
			WithScope("search-test").
			WithEmbedding(e.embedding, "test-model")
		require.NoError(t, db.Entries().Create(ctx, &entry), "create entry %q", e.content)
	}

	// Query vector is identical to the first entry — cosine distance should be 0.
	query := []float32{1.0, 0.0, 0.0, 0.0}
	results, err := db.Entries().SearchSimilar(ctx, query, "search-test", storage.Cosine, 5)
	require.NoError(t, err, "SearchSimilar")
	require.NotEmpty(t, results, "expected at least one result")

	topResult := results[0]
	require.Equal(t, "The sky is blue", topResult.Entry.Content,
		"top result should be the entry with the closest embedding")
	require.Less(t, topResult.Distance, 0.01,
		"cosine distance to identical vector should be ~0, got %f", topResult.Distance)

	// Results should be in ascending distance order.
	for i := 1; i < len(results); i++ {
		require.LessOrEqual(t, results[i-1].Distance, results[i].Distance,
			"results must be ordered by ascending distance")
	}
}

// =============================================================================
// TestAcceptance_GraphTraversal
//
// Creates a three-node chain A -> B -> C connected with "related-to" edges.
// Traverses the graph by following EdgesFrom and EdgesTo to verify the full
// path can be walked in both directions.
// =============================================================================

func TestAcceptance_GraphTraversal(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "graph-test")

	src := testSource("graph-traversal-test")

	entryA := model.NewEntry("Node A — the root concept", src).WithScope("graph-test")
	entryB := model.NewEntry("Node B — derived from A", src).WithScope("graph-test")
	entryC := model.NewEntry("Node C — derived from B", src).WithScope("graph-test")

	for _, e := range []*model.Entry{&entryA, &entryB, &entryC} {
		require.NoError(t, db.Entries().Create(ctx, e), "create entry %q", e.Content)
	}

	// A -> B, B -> C
	edgeAB := model.NewEdge(entryA.ID, entryB.ID, model.EdgeRelatedTo)
	edgeBC := model.NewEdge(entryB.ID, entryC.ID, model.EdgeRelatedTo)

	require.NoError(t, db.Edges().Create(ctx, &edgeAB), "create edge A->B")
	require.NoError(t, db.Edges().Create(ctx, &edgeBC), "create edge B->C")

	// Walk forward from A.
	fromA, err := db.Edges().EdgesFrom(ctx, entryA.ID, storage.EdgeFilter{})
	require.NoError(t, err, "EdgesFrom(A)")
	require.Len(t, fromA, 1, "A should have exactly one outgoing edge")
	require.Equal(t, entryB.ID, fromA[0].ToID, "A's outgoing edge should point to B")

	// Walk forward from B.
	fromB, err := db.Edges().EdgesFrom(ctx, entryB.ID, storage.EdgeFilter{})
	require.NoError(t, err, "EdgesFrom(B)")
	require.Len(t, fromB, 1, "B should have exactly one outgoing edge")
	require.Equal(t, entryC.ID, fromB[0].ToID, "B's outgoing edge should point to C")

	// Verify C has no outgoing edges.
	fromC, err := db.Edges().EdgesFrom(ctx, entryC.ID, storage.EdgeFilter{})
	require.NoError(t, err, "EdgesFrom(C)")
	require.Empty(t, fromC, "C should have no outgoing edges")

	// Walk backward from C — should reach B.
	toC, err := db.Edges().EdgesTo(ctx, entryC.ID, storage.EdgeFilter{})
	require.NoError(t, err, "EdgesTo(C)")
	require.Len(t, toC, 1, "C should have exactly one incoming edge")
	require.Equal(t, entryB.ID, toC[0].FromID, "C's incoming edge should come from B")

	// Walk backward from B — should reach A.
	toB, err := db.Edges().EdgesTo(ctx, entryB.ID, storage.EdgeFilter{})
	require.NoError(t, err, "EdgesTo(B)")
	require.Len(t, toB, 1, "B should have exactly one incoming edge")
	require.Equal(t, entryA.ID, toB[0].FromID, "B's incoming edge should come from A")
}

// =============================================================================
// TestAcceptance_ConflictDetection
//
// Adds two contradicting claims about the same topic and one unrelated entry.
// Links the contradictions with EdgeContradicts edges. Verifies that
// FindConflicts surfaces both conflicting entries and omits the unrelated one.
// =============================================================================

func TestAcceptance_ConflictDetection(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "conflict-test")

	src := testSource("conflict-detection-test")

	claimTrue := model.NewEntry("The service is stateless", src).WithScope("conflict-test")
	claimFalse := model.NewEntry("The service maintains session state", src).WithScope("conflict-test")
	unrelated := model.NewEntry("The service uses HTTP/2", src).WithScope("conflict-test")

	for _, e := range []*model.Entry{&claimTrue, &claimFalse, &unrelated} {
		require.NoError(t, db.Entries().Create(ctx, e), "create entry %q", e.Content)
	}

	// claimTrue contradicts claimFalse (bidirectional semantic, but a single
	// directed edge — FindConflicts checks both directions).
	contradicts := model.NewEdge(claimTrue.ID, claimFalse.ID, model.EdgeContradicts)
	related := model.NewEdge(claimTrue.ID, unrelated.ID, model.EdgeRelatedTo)

	require.NoError(t, db.Edges().Create(ctx, &contradicts), "create contradicts edge")
	require.NoError(t, db.Edges().Create(ctx, &related), "create related-to edge")

	// FindConflicts for claimTrue should return claimFalse only.
	conflicts, err := db.Edges().FindConflicts(ctx, claimTrue.ID)
	require.NoError(t, err, "FindConflicts(claimTrue)")
	require.Len(t, conflicts, 1,
		"expected exactly one conflict for claimTrue, got %d: %v",
		len(conflicts), contentList(conflicts))
	require.Equal(t, claimFalse.ID, conflicts[0].ID,
		"conflicting entry should be claimFalse")

	// FindConflicts for claimFalse should return claimTrue (edge is directional
	// but FindConflicts checks both sides of the "contradicts" edge).
	conflictsReverse, err := db.Edges().FindConflicts(ctx, claimFalse.ID)
	require.NoError(t, err, "FindConflicts(claimFalse)")
	require.Len(t, conflictsReverse, 1,
		"expected claimTrue to be returned when searching from claimFalse side")
	require.Equal(t, claimTrue.ID, conflictsReverse[0].ID,
		"conflicting entry should be claimTrue")

	// The unrelated entry should not appear in any conflict list.
	unrelatedConflicts, err := db.Edges().FindConflicts(ctx, unrelated.ID)
	require.NoError(t, err, "FindConflicts(unrelated)")
	require.Empty(t, unrelatedConflicts,
		"unrelated entry should have no conflicts, got: %v", contentList(unrelatedConflicts))
}

// =============================================================================
// TestAcceptance_TTLAndGC
//
// Creates three entries: one with an already-expired TTL, one with a future
// TTL, and one with no TTL. Verifies that:
//   - The expired entry is excluded from normal List results.
//   - DeleteExpired removes only the expired entry.
//   - The other two entries survive GC.
// =============================================================================

func TestAcceptance_TTLAndGC(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "ttl-test")

	src := testSource("ttl-gc-test")

	// Already expired: ExpiresAt is set to one hour in the past.
	expired := model.NewEntry("This knowledge has decayed", src).WithScope("ttl-test")
	expired.SetTTL(-time.Hour)

	// Still valid: expires in one hour.
	active := model.NewEntry("This knowledge is current", src).WithScope("ttl-test")
	active.SetTTL(time.Hour)

	// No TTL: lives forever.
	immortal := model.NewEntry("This knowledge is permanent", src).WithScope("ttl-test")

	require.NoError(t, db.Entries().Create(ctx, &expired), "create expired entry")
	require.NoError(t, db.Entries().Create(ctx, &active), "create active entry")
	require.NoError(t, db.Entries().Create(ctx, &immortal), "create immortal entry")

	// Confirm expired is hidden from normal listing.
	listed, err := db.Entries().List(ctx, storage.EntryFilter{Scope: "ttl-test"})
	require.NoError(t, err, "List (default, no expired)")
	for _, e := range listed {
		require.NotEqual(t, expired.ID, e.ID,
			"expired entry must not appear in default listing")
	}

	// Run GC.
	deleted, err := db.Entries().DeleteExpired(ctx)
	require.NoError(t, err, "DeleteExpired")
	require.GreaterOrEqual(t, deleted, int64(1),
		"DeleteExpired should have removed at least one entry")

	// Expired entry must be gone.
	_, err = db.Entries().Get(ctx, expired.ID)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"expired entry should be gone after DeleteExpired")

	// Active and immortal entries must survive.
	_, err = db.Entries().Get(ctx, active.ID)
	require.NoError(t, err, "active entry should still exist after GC")

	_, err = db.Entries().Get(ctx, immortal.ID)
	require.NoError(t, err, "immortal entry should still exist after GC")
}

// =============================================================================
// TestAcceptance_Dedup
//
// Submits the same content string twice to the same scope using CreateOrUpdate.
// Verifies that:
//   - The second call does not create a new entry (same ID is returned).
//   - The version is bumped (the update path was taken).
//   - Only one entry exists for that content+scope combination.
// =============================================================================

func TestAcceptance_Dedup(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "dedup-test")

	content := "Go interfaces enable decoupled design"

	firstEntry := model.NewEntry(content, testSource("agent-alpha")).WithScope("dedup-test")
	result1, err := db.Entries().CreateOrUpdate(ctx, &firstEntry)
	require.NoError(t, err, "first CreateOrUpdate")
	require.Equal(t, 1, result1.Version, "first insert should produce version 1")

	originalID := result1.ID

	// Second agent submits the same content.
	secondEntry := model.NewEntry(content, testSource("agent-beta")).WithScope("dedup-test")
	result2, err := db.Entries().CreateOrUpdate(ctx, &secondEntry)
	require.NoError(t, err, "second CreateOrUpdate (dedup)")

	require.Equal(t, originalID, result2.ID,
		"dedup should preserve the original entry ID, not create a new one")
	require.Equal(t, 2, result2.Version,
		"dedup update should increment the version")

	// Confirm only one entry exists in the scope with this content.
	allEntries, err := db.Entries().List(ctx, storage.EntryFilter{Scope: "dedup-test"})
	require.NoError(t, err, "List after dedup")
	require.Len(t, allEntries, 1,
		"should be exactly one entry after dedup, found %d", len(allEntries))
}

// =============================================================================
// TestAcceptance_ScopeIsolation
//
// Creates entries in two sibling scopes with identical embeddings. Searches
// from each scope and verifies that results contain only entries from the
// target scope.
// =============================================================================

func TestAcceptance_ScopeIsolation(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "isolation-alpha")
	ensureScope(t, db, "isolation-beta")

	src := testSource("scope-isolation-test")
	// Both scopes get the same embedding so that without scope filtering the
	// result would be ambiguous — only the filter makes the test deterministic.
	sharedEmbedding := []float32{0.6, 0.8, 0.0}

	alphaEntry := model.NewEntry("Alpha team knowledge", src).
		WithScope("isolation-alpha").
		WithEmbedding(sharedEmbedding, "test-model")
	betaEntry := model.NewEntry("Beta team knowledge", src).
		WithScope("isolation-beta").
		WithEmbedding(sharedEmbedding, "test-model")

	require.NoError(t, db.Entries().Create(ctx, &alphaEntry), "create alpha entry")
	require.NoError(t, db.Entries().Create(ctx, &betaEntry), "create beta entry")

	// Search within isolation-alpha scope.
	alphaResults, err := db.Entries().SearchSimilar(ctx, sharedEmbedding, "isolation-alpha", storage.Cosine, 10)
	require.NoError(t, err, "SearchSimilar in isolation-alpha")
	require.NotEmpty(t, alphaResults, "alpha scope search should return results")
	for _, r := range alphaResults {
		require.Equal(t, "isolation-alpha", r.Entry.Scope,
			"result scope must be isolation-alpha, got %q", r.Entry.Scope)
	}

	// Search within isolation-beta scope.
	betaResults, err := db.Entries().SearchSimilar(ctx, sharedEmbedding, "isolation-beta", storage.Cosine, 10)
	require.NoError(t, err, "SearchSimilar in isolation-beta")
	require.NotEmpty(t, betaResults, "beta scope search should return results")
	for _, r := range betaResults {
		require.Equal(t, "isolation-beta", r.Entry.Scope,
			"result scope must be isolation-beta, got %q", r.Entry.Scope)
	}

	// Confirm the two result sets are disjoint by entry ID.
	alphaIDs := make(map[model.ID]bool)
	for _, r := range alphaResults {
		alphaIDs[r.Entry.ID] = true
	}
	for _, r := range betaResults {
		require.False(t, alphaIDs[r.Entry.ID],
			"beta result %q should not appear in alpha results", r.Entry.Content)
	}
}

// =============================================================================
// TestAcceptance_EdgeCascadeOnDelete
//
// Creates entries A and B with two edges between them (one in each direction)
// and one edge from B to a third entry C. Deletes entry A and verifies:
//   - A is gone (ErrNotFound).
//   - Both edges involving A are gone (ErrNotFound).
//   - The edge from B to C is untouched.
//   - B and C themselves still exist.
// =============================================================================

func TestAcceptance_EdgeCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	ensureScope(t, db, "cascade-test")

	src := testSource("cascade-delete-test")

	entryA := model.NewEntry("Entry A — will be deleted", src).WithScope("cascade-test")
	entryB := model.NewEntry("Entry B — survives", src).WithScope("cascade-test")
	entryC := model.NewEntry("Entry C — survives", src).WithScope("cascade-test")

	for _, e := range []*model.Entry{&entryA, &entryB, &entryC} {
		require.NoError(t, db.Entries().Create(ctx, e), "create entry %q", e.Content)
	}

	// Two edges involving A: A->B and B->A (to verify both directions cascade).
	edgeAtoB := model.NewEdge(entryA.ID, entryB.ID, model.EdgeDependsOn)
	edgeBtoA := model.NewEdge(entryB.ID, entryA.ID, model.EdgeRelatedTo)
	// One edge not involving A.
	edgeBtoC := model.NewEdge(entryB.ID, entryC.ID, model.EdgeElaborates)

	require.NoError(t, db.Edges().Create(ctx, &edgeAtoB), "create edge A->B")
	require.NoError(t, db.Edges().Create(ctx, &edgeBtoA), "create edge B->A")
	require.NoError(t, db.Edges().Create(ctx, &edgeBtoC), "create edge B->C")

	// Verify all three edges exist before deletion.
	_, err := db.Edges().Get(ctx, edgeAtoB.ID)
	require.NoError(t, err, "edge A->B should exist before delete")
	_, err = db.Edges().Get(ctx, edgeBtoA.ID)
	require.NoError(t, err, "edge B->A should exist before delete")
	_, err = db.Edges().Get(ctx, edgeBtoC.ID)
	require.NoError(t, err, "edge B->C should exist before delete")

	// Delete entry A.
	require.NoError(t, db.Entries().Delete(ctx, entryA.ID), "delete entry A")

	// Entry A must be gone.
	_, err = db.Entries().Get(ctx, entryA.ID)
	require.ErrorIs(t, err, storage.ErrNotFound, "entry A should be gone after delete")

	// Both edges that involved A must be cascade-deleted.
	_, err = db.Edges().Get(ctx, edgeAtoB.ID)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"edge A->B should be cascade-deleted when entry A is deleted")

	_, err = db.Edges().Get(ctx, edgeBtoA.ID)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"edge B->A should be cascade-deleted when entry A is deleted")

	// Edge B->C must be untouched.
	survivingEdge, err := db.Edges().Get(ctx, edgeBtoC.ID)
	require.NoError(t, err, "edge B->C should survive deletion of A")
	require.Equal(t, entryB.ID, survivingEdge.FromID,
		"surviving edge from_id should still be B")
	require.Equal(t, entryC.ID, survivingEdge.ToID,
		"surviving edge to_id should still be C")

	// Entries B and C must still exist.
	_, err = db.Entries().Get(ctx, entryB.ID)
	require.NoError(t, err, "entry B should survive deletion of A")

	_, err = db.Entries().Get(ctx, entryC.ID)
	require.NoError(t, err, "entry C should survive deletion of A")
}

// =============================================================================
// Helpers
// =============================================================================

// contentList returns the Content fields of a slice of entries for use in
// error messages.
func contentList(entries []model.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Content
	}
	return out
}
