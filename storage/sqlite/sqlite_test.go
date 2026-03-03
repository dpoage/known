package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/sqlite"
)

// testDB is a shared test database instance.
var testDB *sqlite.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	var err error
	testDB, err = sqlite.New(ctx, ":memory:")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open sqlite: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	if err := testDB.Migrate(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to migrate: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// =============================================================================
// Scope Tests
// =============================================================================

func TestScopeUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	scope := model.NewScope("test-scope-ug")
	scope.Meta = model.Metadata{"key": "value"}

	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := scopes.Get(ctx, "test-scope-ug")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Path != scope.Path {
		t.Errorf("Path = %q, want %q", got.Path, scope.Path)
	}
	if got.Meta.GetString("key") != "value" {
		t.Errorf("Meta[key] = %q, want %q", got.Meta.GetString("key"), "value")
	}
}

func TestScopeGetNotFound(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	_, err := scopes.Get(ctx, "nonexistent-scope")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestScopeUpsertUpdate(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	scope := model.NewScope("test-scope-update")
	scope.Meta = model.Metadata{"version": "1"}
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert(1): %v", err)
	}

	scope.Meta = model.Metadata{"version": "2"}
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert(2): %v", err)
	}

	got, err := scopes.Get(ctx, "test-scope-update")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Meta.GetString("version") != "2" {
		t.Errorf("Meta[version] = %q, want %q", got.Meta.GetString("version"), "2")
	}
}

func TestScopeDelete(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	scope := model.NewScope("test-scope-del")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := scopes.Delete(ctx, "test-scope-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := scopes.Get(ctx, "test-scope-del")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestScopeDeleteNotFound(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	err := scopes.Delete(ctx, "nonexistent-scope-del")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestScopeList(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	prefix := "slisttest"
	paths := []string{
		prefix,
		prefix + ".alpha",
		prefix + ".beta",
	}
	for _, p := range paths {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert(%s): %v", p, err)
		}
	}

	all, err := scopes.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	found := make(map[string]bool)
	for _, s := range all {
		found[s.Path] = true
	}
	for _, p := range paths {
		if !found[p] {
			t.Errorf("scope %q not found in list", p)
		}
	}
}

func TestScopeHierarchy(t *testing.T) {
	ctx := context.Background()
	scopes := testDB.Scopes()

	paths := []string{"shier", "shier.a", "shier.a.x", "shier.b"}
	for _, p := range paths {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert(%s): %v", p, err)
		}
	}

	children, err := scopes.ListChildren(ctx, "shier")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	childPaths := scopePaths(children)
	if !contains(childPaths, "shier.a") || !contains(childPaths, "shier.b") {
		t.Errorf("ListChildren(shier) = %v, want [shier.a, shier.b]", childPaths)
	}
	if contains(childPaths, "shier.a.x") {
		t.Errorf("ListChildren(shier) should not include grandchild shier.a.x")
	}

	desc, err := scopes.ListDescendants(ctx, "shier")
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	descPaths := scopePaths(desc)
	for _, want := range []string{"shier.a", "shier.a.x", "shier.b"} {
		if !contains(descPaths, want) {
			t.Errorf("ListDescendants(shier) missing %q, got %v", want, descPaths)
		}
	}
	if contains(descPaths, "shier") {
		t.Errorf("ListDescendants(shier) should not include ancestor itself")
	}
}

// =============================================================================
// Entry Tests
// =============================================================================

func TestEntryCreateAndGet(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sentrytest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("test knowledge", model.Source{
		Type:      model.SourceFile,
		Reference: "/test.go",
		Meta:      model.Metadata{"line": 42},
	}).WithScope("sentrytest").
		WithMeta(model.Metadata{"tag": "test"}).
		WithEmbedding([]float32{0.1, 0.2, 0.3}, "test-model")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != entry.ID {
		t.Errorf("ID = %v, want %v", got.ID, entry.ID)
	}
	if got.Content != "test knowledge" {
		t.Errorf("Content = %q, want %q", got.Content, "test knowledge")
	}
	if got.Scope != "sentrytest" {
		t.Errorf("Scope = %q, want %q", got.Scope, "sentrytest")
	}
	if got.EmbeddingDim != 3 {
		t.Errorf("EmbeddingDim = %d, want 3", got.EmbeddingDim)
	}
	if got.EmbeddingModel != "test-model" {
		t.Errorf("EmbeddingModel = %q, want %q", got.EmbeddingModel, "test-model")
	}
	if got.Source.Type != model.SourceFile {
		t.Errorf("Source.Type = %q, want %q", got.Source.Type, model.SourceFile)
	}
	if got.Source.Reference != "/test.go" {
		t.Errorf("Source.Reference = %q, want %q", got.Source.Reference, "/test.go")
	}
	if got.Meta.GetString("tag") != "test" {
		t.Errorf("Meta[tag] = %q, want %q", got.Meta.GetString("tag"), "test")
	}
	if got.Provenance.Level != model.ProvenanceInferred {
		t.Errorf("Provenance.Level = %q, want %q", got.Provenance.Level, model.ProvenanceInferred)
	}
}

func TestEntryGetNotFound(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()

	_, err := entries.Get(ctx, model.NewID())
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEntryUpdate(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sentryupdate")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("original", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sentryupdate")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entry.Content = "updated"
	entry.Touch()

	if err := entries.Update(ctx, &entry); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "updated" {
		t.Errorf("Content = %q, want %q", got.Content, "updated")
	}
}

func TestEntryUpdateNotFound(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()

	entry := model.NewEntry("ghost", model.Source{
		Type:      model.SourceManual,
		Reference: "none",
	})

	err := entries.Update(ctx, &entry)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEntryDelete(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sentrydel")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("to delete", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sentrydel")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := entries.Delete(ctx, entry.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := entries.Get(ctx, entry.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestEntryDeleteNotFound(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()

	err := entries.Delete(ctx, model.NewID())
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEntryListByScope(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	for _, p := range []string{"slistscope", "slistscope.sub", "sother-listscope"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("in scope", src).WithScope("slistscope")
	e2 := model.NewEntry("in subscope", src).WithScope("slistscope.sub")
	e3 := model.NewEntry("other scope", src).WithScope("sother-listscope")

	for _, e := range []*model.Entry{&e1, &e2, &e3} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := entries.List(ctx, storage.EntryFilter{Scope: "slistscope"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "in scope" {
		t.Errorf("exact scope: got %d entries, want 1 with 'in scope'", len(got))
	}

	got, err = entries.List(ctx, storage.EntryFilter{ScopePrefix: "slistscope"})
	if err != nil {
		t.Fatalf("List prefix: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("scope prefix: got %d entries, want 2", len(got))
	}
}

func TestEntryListPagination(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("spagtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	for i := 0; i < 5; i++ {
		e := model.NewEntry(fmt.Sprintf("entry-%d", i), src).WithScope("spagtest")
		if err := entries.Create(ctx, &e); err != nil {
			t.Fatalf("Create: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	page1, err := entries.List(ctx, storage.EntryFilter{Scope: "spagtest", Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1: got %d entries, want 2", len(page1))
	}

	page2, err := entries.List(ctx, storage.EntryFilter{Scope: "spagtest", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2: got %d entries, want 2", len(page2))
	}

	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 overlap")
	}
}

func TestEntryTTLExpiration(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sttltest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}

	expired := model.NewEntry("expired", src).WithScope("sttltest")
	expired.SetTTL(-time.Hour)
	if err := entries.Create(ctx, &expired); err != nil {
		t.Fatalf("Create expired: %v", err)
	}

	active := model.NewEntry("active", src).WithScope("sttltest")
	active.SetTTL(time.Hour)
	if err := entries.Create(ctx, &active); err != nil {
		t.Fatalf("Create active: %v", err)
	}

	noTTL := model.NewEntry("no-ttl", src).WithScope("sttltest")
	if err := entries.Create(ctx, &noTTL); err != nil {
		t.Fatalf("Create no-ttl: %v", err)
	}

	got, err := entries.List(ctx, storage.EntryFilter{Scope: "sttltest"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range got {
		if e.Content == "expired" {
			t.Error("expired entry should not appear in default listing")
		}
	}

	got, err = entries.List(ctx, storage.EntryFilter{Scope: "sttltest", IncludeExpired: true})
	if err != nil {
		t.Fatalf("List(IncludeExpired): %v", err)
	}
	foundExpired := false
	for _, e := range got {
		if e.Content == "expired" {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Error("IncludeExpired should show expired entries")
	}

	deleted, err := entries.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted < 1 {
		t.Errorf("DeleteExpired = %d, want >= 1", deleted)
	}

	_, err = entries.Get(ctx, expired.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expired entry should be deleted, got %v", err)
	}

	if _, err := entries.Get(ctx, active.ID); err != nil {
		t.Errorf("active entry should still exist: %v", err)
	}
	if _, err := entries.Get(ctx, noTTL.ID); err != nil {
		t.Errorf("no-ttl entry should still exist: %v", err)
	}
}

func TestEntryWithProvenance(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sprovtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("verified knowledge", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sprovtest").WithProvenance(model.Provenance{
		Level: model.ProvenanceVerified,
	}).WithFreshness(model.Freshness{
		ObservedAt: time.Now(),
		ObservedBy: "admin",
	})

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provenance.Level != model.ProvenanceVerified {
		t.Errorf("Provenance.Level = %q, want %q", got.Provenance.Level, model.ProvenanceVerified)
	}
	if got.Freshness.ObservedBy != "admin" {
		t.Errorf("Freshness.ObservedBy = %q, want %q", got.Freshness.ObservedBy, "admin")
	}
	if got.Freshness.ObservedAt.IsZero() {
		t.Error("Freshness.ObservedAt should not be zero")
	}
}

func TestEntryListByProvenance(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sprovfilter")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	verified := model.NewEntry("verified", src).WithScope("sprovfilter").WithProvenance(model.Provenance{Level: model.ProvenanceVerified})
	inferred := model.NewEntry("inferred", src).WithScope("sprovfilter")

	for _, e := range []*model.Entry{&verified, &inferred} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := entries.List(ctx, storage.EntryFilter{
		Scope:           "sprovfilter",
		ProvenanceLevel: model.ProvenanceVerified,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "verified" {
		t.Errorf("provenance filter: got %d entries, want 1 verified", len(got))
	}
}

// =============================================================================
// Vector Similarity Tests
// =============================================================================

func TestSearchSimilar(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("svectest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}

	e1 := model.NewEntry("similar to query", src).WithScope("svectest").
		WithEmbedding([]float32{1.0, 0.0, 0.0}, "test-model")
	e2 := model.NewEntry("somewhat similar", src).WithScope("svectest").
		WithEmbedding([]float32{0.7, 0.7, 0.0}, "test-model")
	e3 := model.NewEntry("different", src).WithScope("svectest").
		WithEmbedding([]float32{0.0, 0.0, 1.0}, "test-model")
	e4 := model.NewEntry("no embedding", src).WithScope("svectest")

	for _, e := range []*model.Entry{&e1, &e2, &e3, &e4} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	results, err := entries.SearchSimilar(ctx, []float32{1.0, 0.0, 0.0}, "svectest", storage.Cosine, 3)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("SearchSimilar: got %d results, want 3", len(results))
	}

	if results[0].Entry.Content != "similar to query" {
		t.Errorf("first result = %q, want %q", results[0].Entry.Content, "similar to query")
	}

	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Errorf("results not sorted by distance: [%d]=%f < [%d]=%f",
				i, results[i].Distance, i-1, results[i-1].Distance)
		}
	}
}

func TestSearchSimilarScopeFilter(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	for _, p := range []string{"svecscopeA", "svecscopeB"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	emb := []float32{1.0, 0.0, 0.0}

	eA := model.NewEntry("scope A", src).WithScope("svecscopeA").WithEmbedding(emb, "test-model")
	eB := model.NewEntry("scope B", src).WithScope("svecscopeB").WithEmbedding(emb, "test-model")

	for _, e := range []*model.Entry{&eA, &eB} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	results, err := entries.SearchSimilar(ctx, emb, "svecscopeA", storage.Cosine, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}

	for _, r := range results {
		if r.Entry.Scope != "svecscopeA" {
			t.Errorf("result scope = %q, want %q", r.Entry.Scope, "svecscopeA")
		}
	}
}

// =============================================================================
// Edge Tests
// =============================================================================

func TestEdgeCreateAndGet(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sedgetest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("from", src).WithScope("sedgetest")
	e2 := model.NewEntry("to", src).WithScope("sedgetest")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn).WithWeight(0.9).
		WithMeta(model.Metadata{"reason": "test"})

	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	got, err := edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.FromID != e1.ID {
		t.Errorf("FromID = %v, want %v", got.FromID, e1.ID)
	}
	if got.ToID != e2.ID {
		t.Errorf("ToID = %v, want %v", got.ToID, e2.ID)
	}
	if got.Type != model.EdgeDependsOn {
		t.Errorf("Type = %q, want %q", got.Type, model.EdgeDependsOn)
	}
	if got.Weight == nil || *got.Weight != 0.9 {
		t.Errorf("Weight = %v, want 0.9", got.Weight)
	}
	if got.Meta.GetString("reason") != "test" {
		t.Errorf("Meta[reason] = %q, want %q", got.Meta.GetString("reason"), "test")
	}
}

func TestEdgeGetNotFound(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()

	_, err := edges.Get(ctx, model.NewID())
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEdgeDelete(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sedgedel")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("sedgedel")
	e2 := model.NewEntry("b", src).WithScope("sedgedel")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo)
	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	if err := edges.Delete(ctx, edge.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := edges.Get(ctx, edge.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestEdgeDeleteNotFound(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()

	err := edges.Delete(ctx, model.NewID())
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEdgesFromAndTo(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sadjtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("center", src).WithScope("sadjtest")
	e2 := model.NewEntry("dep1", src).WithScope("sadjtest")
	e3 := model.NewEntry("dep2", src).WithScope("sadjtest")
	for _, e := range []*model.Entry{&e1, &e2, &e3} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edges1 := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	edges2 := model.NewEdge(e1.ID, e3.ID, model.EdgeRelatedTo)
	edges3 := model.NewEdge(e3.ID, e1.ID, model.EdgeElaborates)
	for _, e := range []*model.Edge{&edges1, &edges2, &edges3} {
		if err := edges.Create(ctx, e); err != nil {
			t.Fatalf("Create edge: %v", err)
		}
	}

	from, err := edges.EdgesFrom(ctx, e1.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(from) != 2 {
		t.Errorf("EdgesFrom(e1) = %d, want 2", len(from))
	}

	to, err := edges.EdgesTo(ctx, e1.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(to) != 1 {
		t.Errorf("EdgesTo(e1) = %d, want 1", len(to))
	}

	filtered, err := edges.EdgesFrom(ctx, e1.ID, storage.EdgeFilter{Type: model.EdgeDependsOn})
	if err != nil {
		t.Fatalf("EdgesFrom(type): %v", err)
	}
	if len(filtered) != 1 || filtered[0].Type != model.EdgeDependsOn {
		t.Errorf("type filter: got %d edges, want 1 depends-on", len(filtered))
	}
}

func TestEdgesBetween(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sbetweentest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("sbetweentest")
	e2 := model.NewEntry("b", src).WithScope("sbetweentest")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge1 := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	edge2 := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo)
	edge3 := model.NewEdge(e2.ID, e1.ID, model.EdgeElaborates)
	for _, e := range []*model.Edge{&edge1, &edge2, &edge3} {
		if err := edges.Create(ctx, e); err != nil {
			t.Fatalf("Create edge: %v", err)
		}
	}

	between, err := edges.EdgesBetween(ctx, e1.ID, e2.ID)
	if err != nil {
		t.Fatalf("EdgesBetween: %v", err)
	}
	if len(between) != 2 {
		t.Errorf("EdgesBetween(e1, e2) = %d, want 2", len(between))
	}
}

func TestEdgeCustomType(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("scustomedge")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("scustomedge")
	e2 := model.NewEntry("b", src).WithScope("scustomedge")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeType("custom-relationship"))
	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge with custom type: %v", err)
	}

	got, err := edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != "custom-relationship" {
		t.Errorf("Type = %q, want %q", got.Type, "custom-relationship")
	}
}

func TestFindConflicts(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sconflicttest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("claim A", src).WithScope("sconflicttest")
	e2 := model.NewEntry("contradicts A", src).WithScope("sconflicttest")
	e3 := model.NewEntry("also contradicts A", src).WithScope("sconflicttest")
	e4 := model.NewEntry("unrelated", src).WithScope("sconflicttest")
	for _, e := range []*model.Entry{&e1, &e2, &e3, &e4} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	c1 := model.NewEdge(e1.ID, e2.ID, model.EdgeContradicts)
	c2 := model.NewEdge(e3.ID, e1.ID, model.EdgeContradicts)
	r1 := model.NewEdge(e1.ID, e4.ID, model.EdgeRelatedTo)
	for _, e := range []*model.Edge{&c1, &c2, &r1} {
		if err := edges.Create(ctx, e); err != nil {
			t.Fatalf("Create edge: %v", err)
		}
	}

	conflicts, err := edges.FindConflicts(ctx, e1.ID)
	if err != nil {
		t.Fatalf("FindConflicts: %v", err)
	}

	if len(conflicts) != 2 {
		t.Fatalf("FindConflicts = %d entries, want 2", len(conflicts))
	}

	conflictContents := make(map[string]bool)
	for _, c := range conflicts {
		conflictContents[c.Content] = true
	}
	if !conflictContents["contradicts A"] || !conflictContents["also contradicts A"] {
		t.Errorf("FindConflicts results = %v, want [contradicts A, also contradicts A]", conflictContents)
	}
}

func TestEdgeCascadeDeleteOnEntry(t *testing.T) {
	ctx := context.Background()
	edgeStore := testDB.Edges()
	entryStore := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("scascadetest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("will be deleted", src).WithScope("scascadetest")
	e2 := model.NewEntry("stays", src).WithScope("scascadetest")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entryStore.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	if err := edgeStore.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	if err := entryStore.Delete(ctx, e1.ID); err != nil {
		t.Fatalf("Delete entry: %v", err)
	}

	_, err := edgeStore.Get(ctx, edge.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected edge to be cascade-deleted, got %v", err)
	}
}

func TestEdgeUpdate(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sedgeupd")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("from-upd", src).WithScope("sedgeupd")
	e2 := model.NewEntry("to-upd", src).WithScope("sedgeupd")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	// Update weight.
	newWeight := 0.8
	edge.Weight = &newWeight
	edge.Meta = model.Metadata{"updated": true}
	if err := edges.Update(ctx, &edge); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Weight == nil || *got.Weight != 0.8 {
		t.Errorf("Weight = %v, want 0.8", got.Weight)
	}
	if got.Meta.Get("updated") != true {
		t.Errorf("Meta[updated] = %v, want true", got.Meta.Get("updated"))
	}
}

func TestEdgeUpdateNotFound(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()

	edge := model.NewEdge(model.NewID(), model.NewID(), model.EdgeRelatedTo)
	if err := edges.Update(ctx, &edge); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEdgeUpdateWeightBounds(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sedgewb")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("from-wb", src).WithScope("sedgewb")
	e2 := model.NewEntry("to-wb", src).WithScope("sedgewb")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	// Create edge with weight 0.0 (lower bound).
	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.0)
	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	got, err := edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Weight == nil || *got.Weight != 0.0 {
		t.Errorf("Weight = %v, want 0.0", got.Weight)
	}

	// Update to weight 1.0 (upper bound).
	maxWeight := 1.0
	edge.Weight = &maxWeight
	if err := edges.Update(ctx, &edge); err != nil {
		t.Fatalf("Update to 1.0: %v", err)
	}

	got, err = edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Weight == nil || *got.Weight != 1.0 {
		t.Errorf("Weight = %v, want 1.0", got.Weight)
	}
}

func TestEdgeUpdateNilWeight(t *testing.T) {
	ctx := context.Background()
	edges := testDB.Edges()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sedgenilw")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("from-nilw", src).WithScope("sedgenilw")
	e2 := model.NewEntry("to-nilw", src).WithScope("sedgenilw")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	// Create edge with nil weight.
	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo)
	if err := edges.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	got, err := edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Weight != nil {
		t.Errorf("Weight = %v, want nil", got.Weight)
	}

	// Update nil weight to a value.
	w := 0.75
	edge.Weight = &w
	if err := edges.Update(ctx, &edge); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err = edges.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Weight == nil || *got.Weight != 0.75 {
		t.Errorf("Weight = %v, want 0.75", got.Weight)
	}
}

// =============================================================================
// Concurrency / Version Tests
// =============================================================================

func TestEntryVersionOnCreate(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sversioncreate")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("versioned content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sversioncreate")

	if entry.Version != 1 {
		t.Fatalf("NewEntry Version = %d, want 1", entry.Version)
	}

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
}

func TestEntryVersionIncrementOnUpdate(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sversioninc")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("v1 content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sversioninc")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entry.Content = "v2 content"
	entry.Touch()
	if err := entries.Update(ctx, &entry); err != nil {
		t.Fatalf("Update(1): %v", err)
	}
	if entry.Version != 2 {
		t.Errorf("after first update, Version = %d, want 2", entry.Version)
	}

	entry.Content = "v3 content"
	entry.Touch()
	if err := entries.Update(ctx, &entry); err != nil {
		t.Fatalf("Update(2): %v", err)
	}
	if entry.Version != 3 {
		t.Errorf("after second update, Version = %d, want 3", entry.Version)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 3 {
		t.Errorf("DB Version = %d, want 3", got.Version)
	}
	if got.Content != "v3 content" {
		t.Errorf("DB Content = %q, want %q", got.Content, "v3 content")
	}
}

func TestEntryConcurrentModificationDetection(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sconcmod")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("original", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sconcmod")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	agent1, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get(agent1): %v", err)
	}
	agent2, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get(agent2): %v", err)
	}

	agent1.Content = "agent1 update"
	agent1.Touch()
	if err := entries.Update(ctx, agent1); err != nil {
		t.Fatalf("Update(agent1): %v", err)
	}

	agent2.Content = "agent2 update"
	agent2.Touch()
	err = entries.Update(ctx, agent2)
	if err == nil {
		t.Fatal("expected concurrent modification error, got nil")
	}

	if !storage.IsConcurrentModification(err) {
		t.Fatalf("expected ConcurrentModificationError, got %T: %v", err, err)
	}

	var cme *storage.ConcurrentModificationError
	if !errors.As(err, &cme) {
		t.Fatalf("errors.As failed for ConcurrentModificationError")
	}
	if cme.ExpectedVersion != 1 {
		t.Errorf("ConcurrentModificationError.ExpectedVersion = %d, want 1", cme.ExpectedVersion)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get after conflict: %v", err)
	}
	if got.Content != "agent1 update" {
		t.Errorf("Content = %q, want %q", got.Content, "agent1 update")
	}
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
}

func TestEntryContentHash(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("shashtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("hashable content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("shashtest")

	expectedHash := model.ComputeContentHash("hashable content")
	if entry.ContentHash != expectedHash {
		t.Fatalf("ContentHash = %q, want %q", entry.ContentHash, expectedHash)
	}

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ContentHash != expectedHash {
		t.Errorf("DB ContentHash = %q, want %q", got.ContentHash, expectedHash)
	}
}

func TestEntryDuplicateContentSameScopeRejected(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("sduptest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry1 := model.NewEntry("duplicate content", model.Source{
		Type:      model.SourceManual,
		Reference: "test1",
	}).WithScope("sduptest")

	if err := entries.Create(ctx, &entry1); err != nil {
		t.Fatalf("Create(1): %v", err)
	}

	entry2 := model.NewEntry("duplicate content", model.Source{
		Type:      model.SourceManual,
		Reference: "test2",
	}).WithScope("sduptest")

	err := entries.Create(ctx, &entry2)
	if err == nil {
		t.Fatal("expected error for duplicate content+scope, got nil")
	}
}

func TestEntryDuplicateContentDifferentScopeAllowed(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	for _, p := range []string{"sdupscope1", "sdupscope2"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	entry1 := model.NewEntry("same content different scope", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sdupscope1")

	entry2 := model.NewEntry("same content different scope", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sdupscope2")

	if err := entries.Create(ctx, &entry1); err != nil {
		t.Fatalf("Create(1): %v", err)
	}
	if err := entries.Create(ctx, &entry2); err != nil {
		t.Fatalf("Create(2): %v (same content in different scope should be allowed)", err)
	}
}

func TestEntryCreateOrUpdate(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("supserttest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry1 := model.NewEntry("upsert content", model.Source{
		Type:      model.SourceManual,
		Reference: "first-agent",
	}).WithScope("supserttest")

	result1, err := entries.CreateOrUpdate(ctx, &entry1)
	if err != nil {
		t.Fatalf("CreateOrUpdate(1): %v", err)
	}
	if result1.Version != 1 {
		t.Errorf("first insert Version = %d, want 1", result1.Version)
	}
	originalID := result1.ID

	entry2 := model.NewEntry("upsert content", model.Source{
		Type:      model.SourceManual,
		Reference: "second-agent",
	}).WithScope("supserttest")

	result2, err := entries.CreateOrUpdate(ctx, &entry2)
	if err != nil {
		t.Fatalf("CreateOrUpdate(2): %v", err)
	}

	if result2.ID != originalID {
		t.Errorf("upsert should preserve original ID: got %v, want %v", result2.ID, originalID)
	}
	if result2.Version != 2 {
		t.Errorf("upsert Version = %d, want 2", result2.Version)
	}
	if result2.Source.Reference != "second-agent" {
		t.Errorf("Source.Reference = %q, want %q", result2.Source.Reference, "second-agent")
	}
}

// =============================================================================
// Transaction Tests
// =============================================================================

func TestWithTxCommit(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	edges := testDB.Edges()
	scopes := testDB.Scopes()

	scope := model.NewScope("stxcommit")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("tx entry 1", src).WithScope("stxcommit")
	e2 := model.NewEntry("tx entry 2", src).WithScope("stxcommit")

	err := testDB.WithTx(ctx, func(txCtx context.Context) error {
		if err := entries.Create(txCtx, &e1); err != nil {
			return err
		}
		if err := entries.Create(txCtx, &e2); err != nil {
			return err
		}
		edge := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
		return edges.Create(txCtx, &edge)
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got1, err := entries.Get(ctx, e1.ID)
	if err != nil {
		t.Fatalf("Get(e1): %v", err)
	}
	if got1.Content != "tx entry 1" {
		t.Errorf("Content = %q, want %q", got1.Content, "tx entry 1")
	}

	got2, err := entries.Get(ctx, e2.ID)
	if err != nil {
		t.Fatalf("Get(e2): %v", err)
	}
	if got2.Content != "tx entry 2" {
		t.Errorf("Content = %q, want %q", got2.Content, "tx entry 2")
	}

	edgesFrom, err := edges.EdgesFrom(ctx, e1.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(edgesFrom) != 1 {
		t.Errorf("EdgesFrom = %d, want 1", len(edgesFrom))
	}
}

func TestWithTxRollback(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("stxrollback")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("should not persist", src).WithScope("stxrollback")

	err := testDB.WithTx(ctx, func(txCtx context.Context) error {
		if err := entries.Create(txCtx, &e1); err != nil {
			return err
		}
		return fmt.Errorf("simulated failure")
	})
	if err == nil {
		t.Fatal("expected error from WithTx, got nil")
	}

	_, err = entries.Get(ctx, e1.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after rollback, got %v", err)
	}
}

func TestWithTxNested(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("stxnested")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("nested tx entry", src).WithScope("stxnested")

	err := testDB.WithTx(ctx, func(outerCtx context.Context) error {
		return testDB.WithTx(outerCtx, func(innerCtx context.Context) error {
			return entries.Create(innerCtx, &e1)
		})
	})
	if err != nil {
		t.Fatalf("nested WithTx: %v", err)
	}

	got, err := entries.Get(ctx, e1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "nested tx entry" {
		t.Errorf("Content = %q, want %q", got.Content, "nested tx entry")
	}
}

// =============================================================================
// Scope Auto-Creation Tests
// =============================================================================

func TestEntryScopeAutoCreation(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	entry := model.NewEntry("auto scope content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sautoscope.sub.deep")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, path := range []string{"sautoscope", "sautoscope.sub", "sautoscope.sub.deep"} {
		_, err := scopes.Get(ctx, path)
		if err != nil {
			t.Errorf("scope %q should exist after auto-creation, got %v", path, err)
		}
	}
}

func TestEntryScopeAutoCreationIdempotent(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	parent := model.NewScope("sprexisting")
	parent.Meta = model.Metadata{"owner": "admin"}
	if err := scopes.Upsert(ctx, &parent); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	entry := model.NewEntry("child content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("sprexisting.child")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := scopes.Get(ctx, "sprexisting")
	if err != nil {
		t.Fatalf("Get parent scope: %v", err)
	}
	if got.Meta.GetString("owner") != "admin" {
		t.Errorf("parent scope meta[owner] = %q, want %q (should not be clobbered)", got.Meta.GetString("owner"), "admin")
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestInterfaceCompliance(t *testing.T) {
	var _ storage.EntryRepo = (*sqlite.EntryStore)(nil)
	var _ storage.EdgeRepo = (*sqlite.EdgeStore)(nil)
	var _ storage.ScopeRepo = (*sqlite.ScopeStore)(nil)
	var _ storage.Backend = (*sqlite.DB)(nil)
}

// =============================================================================
// Helpers
// =============================================================================

func scopePaths(scopes []model.Scope) []string {
	var paths []string
	for _, s := range scopes {
		paths = append(paths, s.Path)
	}
	return paths
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
