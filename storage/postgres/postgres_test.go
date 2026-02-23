package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/postgres"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testDB is a shared test database instance.
var testDB *postgres.DB

func TestMain(m *testing.M) {
	if os.Getenv("KNOWN_INTEGRATION") == "" {
		fmt.Println("Skipping integration tests (set KNOWN_INTEGRATION=1 to run)")
		os.Exit(0)
	}

	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("known_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start postgres container: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = pgContainer.Terminate(ctx)
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get connection string: %v\n", err)
		os.Exit(1)
	}

	testDB, err = postgres.New(ctx, postgres.Config{
		DSN:      connStr,
		MaxConns: 5,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
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

	// Create several scopes with a unique prefix to avoid interference
	prefix := "listtest"
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

	// Verify our scopes are in the list
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

	// Create a hierarchy: hier > hier.a > hier.a.x, hier.b
	paths := []string{"hier", "hier.a", "hier.a.x", "hier.b"}
	for _, p := range paths {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert(%s): %v", p, err)
		}
	}

	// ListChildren of "hier" should return hier.a and hier.b
	children, err := scopes.ListChildren(ctx, "hier")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	childPaths := scopePaths(children)
	if !contains(childPaths, "hier.a") || !contains(childPaths, "hier.b") {
		t.Errorf("ListChildren(hier) = %v, want [hier.a, hier.b]", childPaths)
	}
	if contains(childPaths, "hier.a.x") {
		t.Errorf("ListChildren(hier) should not include grandchild hier.a.x")
	}

	// ListDescendants of "hier" should return hier.a, hier.a.x, hier.b
	desc, err := scopes.ListDescendants(ctx, "hier")
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	descPaths := scopePaths(desc)
	for _, want := range []string{"hier.a", "hier.a.x", "hier.b"} {
		if !contains(descPaths, want) {
			t.Errorf("ListDescendants(hier) missing %q, got %v", want, descPaths)
		}
	}
	if contains(descPaths, "hier") {
		t.Errorf("ListDescendants(hier) should not include ancestor itself")
	}
}

// =============================================================================
// Entry Tests
// =============================================================================

func TestEntryCreateAndGet(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	// Ensure scope exists
	scope := model.NewScope("entrytest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("test knowledge", model.Source{
		Type:      model.SourceFile,
		Reference: "/test.go",
		Meta:      model.Metadata{"line": 42},
	}).WithScope("entrytest").
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
	if got.Scope != "entrytest" {
		t.Errorf("Scope = %q, want %q", got.Scope, "entrytest")
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

	scope := model.NewScope("entryupdate")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("original", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("entryupdate")

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

	scope := model.NewScope("entrydel")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("to delete", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("entrydel")

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

	for _, p := range []string{"listscope", "listscope.sub", "other-listscope"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("in scope", src).WithScope("listscope")
	e2 := model.NewEntry("in subscope", src).WithScope("listscope.sub")
	e3 := model.NewEntry("other scope", src).WithScope("other-listscope")

	for _, e := range []*model.Entry{&e1, &e2, &e3} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Exact scope match
	got, err := entries.List(ctx, storage.EntryFilter{Scope: "listscope"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "in scope" {
		t.Errorf("exact scope: got %d entries, want 1 with 'in scope'", len(got))
	}

	// Scope prefix match (listscope and listscope.sub)
	got, err = entries.List(ctx, storage.EntryFilter{ScopePrefix: "listscope"})
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

	scope := model.NewScope("pagtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	for i := 0; i < 5; i++ {
		e := model.NewEntry(fmt.Sprintf("entry-%d", i), src).WithScope("pagtest")
		if err := entries.Create(ctx, &e); err != nil {
			t.Fatalf("Create: %v", err)
		}
		time.Sleep(time.Millisecond) // ensure distinct created_at for ordering
	}

	// First page
	page1, err := entries.List(ctx, storage.EntryFilter{Scope: "pagtest", Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1: got %d entries, want 2", len(page1))
	}

	// Second page
	page2, err := entries.List(ctx, storage.EntryFilter{Scope: "pagtest", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2: got %d entries, want 2", len(page2))
	}

	// Pages should not overlap
	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 overlap")
	}
}

func TestEntryTTLExpiration(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("ttltest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}

	// Create an expired entry (TTL of -1 hour)
	expired := model.NewEntry("expired", src).WithScope("ttltest")
	expired.SetTTL(-time.Hour)
	if err := entries.Create(ctx, &expired); err != nil {
		t.Fatalf("Create expired: %v", err)
	}

	// Create a non-expired entry
	active := model.NewEntry("active", src).WithScope("ttltest")
	active.SetTTL(time.Hour)
	if err := entries.Create(ctx, &active); err != nil {
		t.Fatalf("Create active: %v", err)
	}

	// Create an entry with no TTL
	noTTL := model.NewEntry("no-ttl", src).WithScope("ttltest")
	if err := entries.Create(ctx, &noTTL); err != nil {
		t.Fatalf("Create no-ttl: %v", err)
	}

	// List should exclude expired by default
	got, err := entries.List(ctx, storage.EntryFilter{Scope: "ttltest"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range got {
		if e.Content == "expired" {
			t.Error("expired entry should not appear in default listing")
		}
	}

	// List with IncludeExpired should include all
	got, err = entries.List(ctx, storage.EntryFilter{Scope: "ttltest", IncludeExpired: true})
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

	// DeleteExpired should remove the expired entry
	deleted, err := entries.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted < 1 {
		t.Errorf("DeleteExpired = %d, want >= 1", deleted)
	}

	// Verify the expired entry is gone
	_, err = entries.Get(ctx, expired.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expired entry should be deleted, got %v", err)
	}

	// Active and no-TTL entries should still exist
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

	scope := model.NewScope("provtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("verified knowledge", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("provtest").WithProvenance(model.Provenance{
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

	scope := model.NewScope("provfilter")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	verified := model.NewEntry("verified", src).WithScope("provfilter").WithProvenance(model.Provenance{Level: model.ProvenanceVerified})
	inferred := model.NewEntry("inferred", src).WithScope("provfilter")

	for _, e := range []*model.Entry{&verified, &inferred} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := entries.List(ctx, storage.EntryFilter{
		Scope:           "provfilter",
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

	scope := model.NewScope("vectest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}

	// Create entries with known embeddings
	e1 := model.NewEntry("similar to query", src).WithScope("vectest").
		WithEmbedding([]float32{1.0, 0.0, 0.0}, "test-model")
	e2 := model.NewEntry("somewhat similar", src).WithScope("vectest").
		WithEmbedding([]float32{0.7, 0.7, 0.0}, "test-model")
	e3 := model.NewEntry("different", src).WithScope("vectest").
		WithEmbedding([]float32{0.0, 0.0, 1.0}, "test-model")
	// Entry without embedding should not appear
	e4 := model.NewEntry("no embedding", src).WithScope("vectest")

	for _, e := range []*model.Entry{&e1, &e2, &e3, &e4} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Search for entries similar to [1, 0, 0]
	results, err := entries.SearchSimilar(ctx, []float32{1.0, 0.0, 0.0}, "vectest", storage.Cosine, 3)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("SearchSimilar: got %d results, want 3", len(results))
	}

	// First result should be the most similar (exact match)
	if results[0].Entry.Content != "similar to query" {
		t.Errorf("first result = %q, want %q", results[0].Entry.Content, "similar to query")
	}

	// Distances should be in ascending order
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

	for _, p := range []string{"vecscopeA", "vecscopeB"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	emb := []float32{1.0, 0.0, 0.0}

	eA := model.NewEntry("scope A", src).WithScope("vecscopeA").WithEmbedding(emb, "test-model")
	eB := model.NewEntry("scope B", src).WithScope("vecscopeB").WithEmbedding(emb, "test-model")

	for _, e := range []*model.Entry{&eA, &eB} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	results, err := entries.SearchSimilar(ctx, emb, "vecscopeA", storage.Cosine, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}

	for _, r := range results {
		if r.Entry.Scope != "vecscopeA" {
			t.Errorf("result scope = %q, want %q", r.Entry.Scope, "vecscopeA")
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

	scope := model.NewScope("edgetest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("from", src).WithScope("edgetest")
	e2 := model.NewEntry("to", src).WithScope("edgetest")
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

	scope := model.NewScope("edgedel")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("edgedel")
	e2 := model.NewEntry("b", src).WithScope("edgedel")
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

	scope := model.NewScope("adjtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("center", src).WithScope("adjtest")
	e2 := model.NewEntry("dep1", src).WithScope("adjtest")
	e3 := model.NewEntry("dep2", src).WithScope("adjtest")
	for _, e := range []*model.Entry{&e1, &e2, &e3} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	// e1 -> e2 (depends-on), e1 -> e3 (related-to), e3 -> e1 (elaborates)
	edges1 := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	edges2 := model.NewEdge(e1.ID, e3.ID, model.EdgeRelatedTo)
	edges3 := model.NewEdge(e3.ID, e1.ID, model.EdgeElaborates)
	for _, e := range []*model.Edge{&edges1, &edges2, &edges3} {
		if err := edges.Create(ctx, e); err != nil {
			t.Fatalf("Create edge: %v", err)
		}
	}

	// EdgesFrom(e1) should return 2 edges
	from, err := edges.EdgesFrom(ctx, e1.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(from) != 2 {
		t.Errorf("EdgesFrom(e1) = %d, want 2", len(from))
	}

	// EdgesTo(e1) should return 1 edge
	to, err := edges.EdgesTo(ctx, e1.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(to) != 1 {
		t.Errorf("EdgesTo(e1) = %d, want 1", len(to))
	}

	// Filter by type
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

	scope := model.NewScope("betweentest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("betweentest")
	e2 := model.NewEntry("b", src).WithScope("betweentest")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge1 := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	edge2 := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo)
	edge3 := model.NewEdge(e2.ID, e1.ID, model.EdgeElaborates) // opposite direction
	for _, e := range []*model.Edge{&edge1, &edge2, &edge3} {
		if err := edges.Create(ctx, e); err != nil {
			t.Fatalf("Create edge: %v", err)
		}
	}

	// e1 -> e2 should return 2 edges
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

	scope := model.NewScope("customedge")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("a", src).WithScope("customedge")
	e2 := model.NewEntry("b", src).WithScope("customedge")
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

	scope := model.NewScope("conflicttest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("claim A", src).WithScope("conflicttest")
	e2 := model.NewEntry("contradicts A", src).WithScope("conflicttest")
	e3 := model.NewEntry("also contradicts A", src).WithScope("conflicttest")
	e4 := model.NewEntry("unrelated", src).WithScope("conflicttest")
	for _, e := range []*model.Entry{&e1, &e2, &e3, &e4} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	// e1 contradicts e2, e3 contradicts e1
	c1 := model.NewEdge(e1.ID, e2.ID, model.EdgeContradicts)
	c2 := model.NewEdge(e3.ID, e1.ID, model.EdgeContradicts)
	// e1 -> e4 is related, not contradicts
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

	scope := model.NewScope("cascadetest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("will be deleted", src).WithScope("cascadetest")
	e2 := model.NewEntry("stays", src).WithScope("cascadetest")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entryStore.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	if err := edgeStore.Create(ctx, &edge); err != nil {
		t.Fatalf("Create edge: %v", err)
	}

	// Delete entry e1 - edge should be cascade-deleted
	if err := entryStore.Delete(ctx, e1.ID); err != nil {
		t.Fatalf("Delete entry: %v", err)
	}

	_, err := edgeStore.Get(ctx, edge.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected edge to be cascade-deleted, got %v", err)
	}
}

// =============================================================================
// Concurrency / Version Tests
// =============================================================================

func TestEntryVersionOnCreate(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("versioncreate")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("versioned content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("versioncreate")

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

	scope := model.NewScope("versioninc")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("v1 content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("versioninc")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First update: version 1 -> 2
	entry.Content = "v2 content"
	entry.Touch()
	if err := entries.Update(ctx, &entry); err != nil {
		t.Fatalf("Update(1): %v", err)
	}
	if entry.Version != 2 {
		t.Errorf("after first update, Version = %d, want 2", entry.Version)
	}

	// Second update: version 2 -> 3
	entry.Content = "v3 content"
	entry.Touch()
	if err := entries.Update(ctx, &entry); err != nil {
		t.Fatalf("Update(2): %v", err)
	}
	if entry.Version != 3 {
		t.Errorf("after second update, Version = %d, want 3", entry.Version)
	}

	// Verify in DB
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

	scope := model.NewScope("concmod")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("original", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("concmod")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate two agents reading the same entry
	agent1, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get(agent1): %v", err)
	}
	agent2, err := entries.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get(agent2): %v", err)
	}

	// Agent 1 updates successfully
	agent1.Content = "agent1 update"
	agent1.Touch()
	if err := entries.Update(ctx, agent1); err != nil {
		t.Fatalf("Update(agent1): %v", err)
	}

	// Agent 2 tries to update with stale version -- should fail
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

	// Verify the DB has agent1's update
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

	scope := model.NewScope("hashtest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry := model.NewEntry("hashable content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("hashtest")

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

	scope := model.NewScope("duptest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	entry1 := model.NewEntry("duplicate content", model.Source{
		Type:      model.SourceManual,
		Reference: "test1",
	}).WithScope("duptest")

	if err := entries.Create(ctx, &entry1); err != nil {
		t.Fatalf("Create(1): %v", err)
	}

	// Second entry with same content and scope should fail
	entry2 := model.NewEntry("duplicate content", model.Source{
		Type:      model.SourceManual,
		Reference: "test2",
	}).WithScope("duptest")

	err := entries.Create(ctx, &entry2)
	if err == nil {
		t.Fatal("expected error for duplicate content+scope, got nil")
	}
}

func TestEntryDuplicateContentDifferentScopeAllowed(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	for _, p := range []string{"dupscope1", "dupscope2"} {
		s := model.NewScope(p)
		if err := scopes.Upsert(ctx, &s); err != nil {
			t.Fatalf("Upsert scope(%s): %v", p, err)
		}
	}

	entry1 := model.NewEntry("same content different scope", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("dupscope1")

	entry2 := model.NewEntry("same content different scope", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("dupscope2")

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

	scope := model.NewScope("upserttest")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	// First call: should insert
	entry1 := model.NewEntry("upsert content", model.Source{
		Type:      model.SourceManual,
		Reference: "first-agent",
	}).WithScope("upserttest")

	result1, err := entries.CreateOrUpdate(ctx, &entry1)
	if err != nil {
		t.Fatalf("CreateOrUpdate(1): %v", err)
	}
	if result1.Version != 1 {
		t.Errorf("first insert Version = %d, want 1", result1.Version)
	}
	originalID := result1.ID

	// Second call with same content: should update (not create duplicate)
	entry2 := model.NewEntry("upsert content", model.Source{
		Type:      model.SourceManual,
		Reference: "second-agent",
	}).WithScope("upserttest")

	result2, err := entries.CreateOrUpdate(ctx, &entry2)
	if err != nil {
		t.Fatalf("CreateOrUpdate(2): %v", err)
	}

	// Should have the original ID (not a new one)
	if result2.ID != originalID {
		t.Errorf("upsert should preserve original ID: got %v, want %v", result2.ID, originalID)
	}
	// Version should be incremented
	if result2.Version != 2 {
		t.Errorf("upsert Version = %d, want 2", result2.Version)
	}
	// Source should be updated
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

	scope := model.NewScope("txcommit")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("tx entry 1", src).WithScope("txcommit")
	e2 := model.NewEntry("tx entry 2", src).WithScope("txcommit")

	// Create entries + edge atomically
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

	// All should be visible
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

	scope := model.NewScope("txrollback")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("should not persist", src).WithScope("txrollback")

	// Transaction that creates an entry then errors out
	err := testDB.WithTx(ctx, func(txCtx context.Context) error {
		if err := entries.Create(txCtx, &e1); err != nil {
			return err
		}
		return fmt.Errorf("simulated failure")
	})
	if err == nil {
		t.Fatal("expected error from WithTx, got nil")
	}

	// Entry should NOT exist
	_, err = entries.Get(ctx, e1.ID)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after rollback, got %v", err)
	}
}

func TestWithTxNested(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	scopes := testDB.Scopes()

	scope := model.NewScope("txnested")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("nested tx entry", src).WithScope("txnested")

	// Nested WithTx should reuse the outer transaction
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

	// Do NOT manually create the scope hierarchy. Entry.Create should handle it.
	entry := model.NewEntry("auto scope content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("autoscope.sub.deep")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the full scope hierarchy was created
	for _, path := range []string{"autoscope", "autoscope.sub", "autoscope.sub.deep"} {
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

	// Pre-create a parent scope with metadata
	parent := model.NewScope("prexisting")
	parent.Meta = model.Metadata{"owner": "admin"}
	if err := scopes.Upsert(ctx, &parent); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Create entry under prexisting.child -- should NOT clobber parent's metadata
	entry := model.NewEntry("child content", model.Source{
		Type:      model.SourceManual,
		Reference: "test",
	}).WithScope("prexisting.child")

	if err := entries.Create(ctx, &entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify parent metadata is preserved (EnsureHierarchy uses ON CONFLICT DO NOTHING)
	got, err := scopes.Get(ctx, "prexisting")
	if err != nil {
		t.Fatalf("Get parent scope: %v", err)
	}
	if got.Meta.GetString("owner") != "admin" {
		t.Errorf("parent scope meta[owner] = %q, want %q (should not be clobbered)", got.Meta.GetString("owner"), "admin")
	}
}

// =============================================================================
// Edge Transaction Atomicity Tests
// =============================================================================

func TestEdgeCreateAtomicWithTx(t *testing.T) {
	ctx := context.Background()
	entries := testDB.Entries()
	edges := testDB.Edges()
	scopes := testDB.Scopes()

	scope := model.NewScope("edgetx")
	if err := scopes.Upsert(ctx, &scope); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("edge tx from", src).WithScope("edgetx")
	e2 := model.NewEntry("edge tx to", src).WithScope("edgetx")
	for _, e := range []*model.Entry{&e1, &e2} {
		if err := entries.Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	// Create two edges atomically
	edge1 := model.NewEdge(e1.ID, e2.ID, model.EdgeDependsOn)
	edge2 := model.NewEdge(e2.ID, e1.ID, model.EdgeRelatedTo)

	err := testDB.WithTx(ctx, func(txCtx context.Context) error {
		if err := edges.Create(txCtx, &edge1); err != nil {
			return err
		}
		return edges.Create(txCtx, &edge2)
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// Both edges should exist
	got1, err := edges.Get(ctx, edge1.ID)
	if err != nil {
		t.Fatalf("Get(edge1): %v", err)
	}
	if got1.Type != model.EdgeDependsOn {
		t.Errorf("edge1 Type = %q, want %q", got1.Type, model.EdgeDependsOn)
	}

	got2, err := edges.Get(ctx, edge2.ID)
	if err != nil {
		t.Fatalf("Get(edge2): %v", err)
	}
	if got2.Type != model.EdgeRelatedTo {
		t.Errorf("edge2 Type = %q, want %q", got2.Type, model.EdgeRelatedTo)
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestInterfaceCompliance(t *testing.T) {
	// Compile-time checks that implementations satisfy interfaces.
	var _ storage.EntryRepo = (*postgres.EntryStore)(nil)
	var _ storage.EdgeRepo = (*postgres.EdgeStore)(nil)
	var _ storage.ScopeRepo = (*postgres.ScopeStore)(nil)
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
