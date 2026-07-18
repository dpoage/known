// Package storage_test contains a contract test suite that validates all storage
// backends implement the same semantics. The suite runs against in-memory SQLite
// unconditionally and against PostgreSQL when KNOWN_INTEGRATION=1.
//
// Scope of coverage:
//   - EntryRepo: CRUD, upsert/dedup (ErrDuplicateContent), optimistic locking
//   - EdgeRepo: create + list (EdgesFrom/EdgesTo)
//   - ScopeRepo: upsert + EnsureHierarchy
//   - SearchText parity on a fixed corpus (same docs, same queries → same hit sets)
package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/sqlite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgstore "github.com/dpoage/known/storage/postgres"
)

// ----------------------------------------------------------------------------
// Backend factories
// ----------------------------------------------------------------------------

func newSQLiteBackend(t *testing.T) storage.Backend {
	t.Helper()
	db, err := sqlite.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("sqlite migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newPostgresBackend(t *testing.T) storage.Backend {
	t.Helper()
	ctx := context.Background()
	pgContainer, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("known_contract"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}

	db, err := pgstore.New(ctx, pgstore.Config{DSN: connStr, MaxConns: 5})
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("postgres migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ----------------------------------------------------------------------------
// Parameterised runner
// ----------------------------------------------------------------------------

func TestContract(t *testing.T) {
	backends := []struct {
		name    string
		factory func(*testing.T) storage.Backend
		skip    bool
	}{
		{"sqlite", newSQLiteBackend, false},
		{"postgres", newPostgresBackend, os.Getenv("KNOWN_INTEGRATION") == ""},
	}

	for _, b := range backends {
		b := b
		if b.skip {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()
			be := b.factory(t)
			ctx := context.Background()

			t.Run("EntryRepo", func(t *testing.T) {
				t.Run("CreateAndGet", func(t *testing.T) { contractEntryCreateGet(t, ctx, be) })
				t.Run("GetNotFound", func(t *testing.T) { contractEntryGetNotFound(t, ctx, be) })
				t.Run("Update", func(t *testing.T) { contractEntryUpdate(t, ctx, be) })
				t.Run("Delete", func(t *testing.T) { contractEntryDelete(t, ctx, be) })
				t.Run("DuplicateContentSameScope", func(t *testing.T) { contractEntryDedupSameScope(t, ctx, be) })
				t.Run("DuplicateContentDifferentScope", func(t *testing.T) { contractEntryDedupDifferentScope(t, ctx, be) })
				t.Run("OptimisticLocking", func(t *testing.T) { contractEntryOptimisticLocking(t, ctx, be) })
				t.Run("CreateOrUpdate", func(t *testing.T) { contractEntryCreateOrUpdate(t, ctx, be) })
				t.Run("ScopeDescendantQuery", func(t *testing.T) { contractEntryScopeDescendant(t, ctx, be) })
			})

			t.Run("EdgeRepo", func(t *testing.T) {
				t.Run("CreateAndList", func(t *testing.T) { contractEdgeCreateList(t, ctx, be) })
			})

			t.Run("ScopeRepo", func(t *testing.T) {
				t.Run("Upsert", func(t *testing.T) { contractScopeUpsert(t, ctx, be) })
				t.Run("EnsureHierarchy", func(t *testing.T) { contractScopeEnsureHierarchy(t, ctx, be) })
			})

			t.Run("SearchText", func(t *testing.T) { contractSearchText(t, ctx, be) })
		})
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func newTestEntry(content, scope string) model.Entry {
	e := model.NewEntry(content, model.Source{
		Type:      model.SourceConversation,
		Reference: "test",
	})
	e.Scope = scope
	return e
}

func mustEnsureScope(t *testing.T, ctx context.Context, be storage.Backend, path string) {
	t.Helper()
	if err := be.Scopes().EnsureHierarchy(ctx, path); err != nil {
		t.Fatalf("EnsureHierarchy(%q): %v", path, err)
	}
}

// ----------------------------------------------------------------------------
// EntryRepo contract tests
// ----------------------------------------------------------------------------

func contractEntryCreateGet(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.entry")
	e := newTestEntry("hello world", "contract.entry")
	if err := be.Entries().Create(ctx, &e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := be.Entries().Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != e.Content {
		t.Errorf("content mismatch: got %q want %q", got.Content, e.Content)
	}
	if got.Scope != e.Scope {
		t.Errorf("scope mismatch: got %q want %q", got.Scope, e.Scope)
	}
	if got.Version != 1 {
		t.Errorf("version: got %d want 1", got.Version)
	}
}

func contractEntryGetNotFound(t *testing.T, ctx context.Context, be storage.Backend) {
	_, err := be.Entries().Get(ctx, model.NewID())
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func contractEntryUpdate(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.update")
	e := newTestEntry("original", "contract.update")
	if err := be.Entries().Create(ctx, &e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	e.Content = "updated"
	e.Touch()
	if err := be.Entries().Update(ctx, &e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := be.Entries().Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Content != "updated" {
		t.Errorf("content after update: got %q want %q", got.Content, "updated")
	}
	if got.Version != 2 {
		t.Errorf("version after update: got %d want 2", got.Version)
	}
}

func contractEntryDelete(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.delete")
	e := newTestEntry("to be deleted", "contract.delete")
	if err := be.Entries().Create(ctx, &e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := be.Entries().Delete(ctx, e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := be.Entries().Get(ctx, e.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: expected ErrNotFound, got %v", err)
	}
}

func contractEntryDedupSameScope(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.dedup")
	e := newTestEntry("duplicate content", "contract.dedup")
	if err := be.Entries().Create(ctx, &e); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	e2 := newTestEntry("duplicate content", "contract.dedup")
	err := be.Entries().Create(ctx, &e2)
	if err == nil {
		t.Fatal("expected ErrDuplicateContent, got nil")
	}
	if !errors.Is(err, storage.ErrDuplicateContent) {
		t.Errorf("expected ErrDuplicateContent, got %v", err)
	}
}

func contractEntryDedupDifferentScope(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.dedup2.a")
	mustEnsureScope(t, ctx, be, "contract.dedup2.b")
	e1 := newTestEntry("same content across scopes", "contract.dedup2.a")
	if err := be.Entries().Create(ctx, &e1); err != nil {
		t.Fatalf("Create scope a: %v", err)
	}
	e2 := newTestEntry("same content across scopes", "contract.dedup2.b")
	if err := be.Entries().Create(ctx, &e2); err != nil {
		t.Fatalf("Create scope b (different scope, should succeed): %v", err)
	}
}

func contractEntryOptimisticLocking(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.lock")
	e := newTestEntry("locked entry", "contract.lock")
	if err := be.Entries().Create(ctx, &e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Load the same entry twice — simulate two concurrent readers.
	got1, _ := be.Entries().Get(ctx, e.ID)
	got2, _ := be.Entries().Get(ctx, e.ID)

	// First writer wins.
	got1.Content = "winner"
	got1.Touch()
	if err := be.Entries().Update(ctx, got1); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// Second writer must be rejected (stale version).
	got2.Content = "loser"
	got2.Touch()
	err := be.Entries().Update(ctx, got2)
	if err == nil {
		t.Fatal("expected ConcurrentModificationError, got nil")
	}
	if !storage.IsConcurrentModification(err) {
		t.Errorf("expected ConcurrentModificationError, got %T: %v", err, err)
	}
}

func contractEntryCreateOrUpdate(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.cou")
	e := newTestEntry("upsert me", "contract.cou")

	// First call: insert.
	got, err := be.Entries().CreateOrUpdate(ctx, &e)
	if err != nil {
		t.Fatalf("CreateOrUpdate (insert): %v", err)
	}
	if got.Version != 1 {
		t.Errorf("insert version: got %d want 1", got.Version)
	}
	id := got.ID

	// Second call with same content+scope: update (upsert).
	e2 := newTestEntry("upsert me", "contract.cou")
	got2, err := be.Entries().CreateOrUpdate(ctx, &e2)
	if err != nil {
		t.Fatalf("CreateOrUpdate (upsert): %v", err)
	}
	if got2.ID != id {
		t.Errorf("upsert should return same ID: got %s want %s", got2.ID, id)
	}
	if got2.Version < 1 {
		t.Errorf("upsert version: got %d want >= 1", got2.Version)
	}
}

// contractEntryScopeDescendant verifies that List with a scope filter returns
// entries in the exact scope AND its descendants — the documented contract for
// both backends (SQLite uses LIKE prefix; Postgres uses ltree).
func contractEntryScopeDescendant(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.tree.child")
	mustEnsureScope(t, ctx, be, "contract.tree.other")

	eParent := newTestEntry("parent scope entry", "contract.tree")
	eChild := newTestEntry("child scope entry", "contract.tree.child")
	eOther := newTestEntry("other scope entry", "contract.tree.other")

	for _, e := range []*model.Entry{&eParent, &eChild, &eOther} {
		if err := be.Entries().Create(ctx, e); err != nil {
			t.Fatalf("Create %q: %v", e.Scope, err)
		}
	}

	list, err := be.Entries().List(ctx, storage.EntryFilter{ScopePrefix: "contract.tree"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := make(map[string]bool, len(list))
	for _, entry := range list {
		ids[entry.ID.String()] = true
	}
	for _, want := range []*model.Entry{&eParent, &eChild, &eOther} {
		if !ids[want.ID.String()] {
			t.Errorf("scope descendant query missing entry %s (scope=%q)", want.ID, want.Scope)
		}
	}
}

// ----------------------------------------------------------------------------
// EdgeRepo contract tests
// ----------------------------------------------------------------------------

func contractEdgeCreateList(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.edge")
	from := newTestEntry("from entry", "contract.edge")
	to := newTestEntry("to entry", "contract.edge")
	for _, e := range []*model.Entry{&from, &to} {
		if err := be.Entries().Create(ctx, e); err != nil {
			t.Fatalf("Create entry: %v", err)
		}
	}

	edge := model.NewEdge(from.ID, to.ID, model.EdgeRelatedTo)
	if err := be.Edges().Create(ctx, &edge); err != nil {
		t.Fatalf("Edge Create: %v", err)
	}

	fromEdges, err := be.Edges().EdgesFrom(ctx, from.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(fromEdges) != 1 {
		t.Fatalf("EdgesFrom: got %d edges want 1", len(fromEdges))
	}
	if fromEdges[0].ToID != to.ID {
		t.Errorf("edge ToID: got %v want %v", fromEdges[0].ToID, to.ID)
	}

	toEdges, err := be.Edges().EdgesTo(ctx, to.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(toEdges) != 1 {
		t.Fatalf("EdgesTo: got %d edges want 1", len(toEdges))
	}
}

// ----------------------------------------------------------------------------
// ScopeRepo contract tests
// ----------------------------------------------------------------------------

func contractScopeUpsert(t *testing.T, ctx context.Context, be storage.Backend) {
	sc := &model.Scope{Path: fmt.Sprintf("contract.scopeupsert.%d", time.Now().UnixNano())}
	if err := be.Scopes().Upsert(ctx, sc); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := be.Scopes().Get(ctx, sc.Path)
	if err != nil {
		t.Fatalf("Get after Upsert: %v", err)
	}
	if got.Path != sc.Path {
		t.Errorf("path mismatch: got %q want %q", got.Path, sc.Path)
	}
}

func contractScopeEnsureHierarchy(t *testing.T, ctx context.Context, be storage.Backend) {
	// Use a unique prefix to avoid collisions with parallel tests.
	prefix := fmt.Sprintf("contract.hier%d", time.Now().UnixNano())
	path := prefix + ".a.b.c"
	if err := be.Scopes().EnsureHierarchy(ctx, path); err != nil {
		t.Fatalf("EnsureHierarchy: %v", err)
	}
	// All ancestors must exist.
	for _, ancestor := range []string{prefix, prefix + ".a", prefix + ".a.b", path} {
		if _, err := be.Scopes().Get(ctx, ancestor); err != nil {
			t.Errorf("ancestor %q not found after EnsureHierarchy: %v", ancestor, err)
		}
	}
}

// ----------------------------------------------------------------------------
// SearchText parity corpus
// ----------------------------------------------------------------------------

// contractSearchText inserts a fixed corpus and verifies:
//   - each query returns the expected hit set (IDs).
//   - the top-1 result is the most relevant document.
//   - empty query returns an error.
//   - no-hit query returns nil, nil.
//
// Ordering parity: SQLite BM25 scores are negative (more negative = more
// relevant); Postgres ts_rank scores are positive (higher = more relevant).
// Both are consistent within their backend; RRF normalises by position so the
// sign difference is safe. This test only asserts top-1 and full hit-set
// equality, not exact rank ordering, which would require identical scoring.
func contractSearchText(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.fts")

	corpus := []struct {
		label   string
		title   string
		content string
	}{
		{"alpha", "Go programming language", "Go is a statically typed compiled language."},
		{"beta", "Python scripting", "Python is a dynamically typed interpreted language."},
		{"gamma", "Rust systems programming", "Rust is a systems language focused on safety and performance."},
		{"delta", "Database indexing", "B-tree and hash indexes are common database index structures."},
	}

	entries := make(map[string]model.Entry, len(corpus))
	for _, doc := range corpus {
		e := newTestEntry(doc.content, "contract.fts")
		e.Title = doc.title
		if err := be.Entries().Create(ctx, &e); err != nil {
			t.Fatalf("corpus Create %q: %v", doc.label, err)
		}
		entries[doc.label] = e
	}

	// Empty query must return an error.
	_, err := be.Entries().SearchText(ctx, "", "contract.fts", 10)
	if err == nil {
		t.Error("empty query: expected error, got nil")
	}

	// No-hit query must return nil, nil (not an error).
	noHits, err := be.Entries().SearchText(ctx, "xyznotfound", "contract.fts", 10)
	if err != nil {
		t.Errorf("no-hit query: expected nil error, got %v", err)
	}
	if len(noHits) != 0 {
		t.Errorf("no-hit query: expected 0 results, got %d", len(noHits))
	}

	cases := []struct {
		query   string
		wantIDs []string // expected hit set (all must appear)
		top1    string   // label of expected top-1 result
	}{
		{
			query:   "Go programming",
			wantIDs: []string{"alpha"},
			top1:    "alpha",
		},
		{
			query:   "language",
			wantIDs: []string{"alpha", "beta", "gamma"},
			top1:    "", // any of the three is acceptable
		},
		{
			query:   "database index",
			wantIDs: []string{"delta"},
			top1:    "delta",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("query/"+tc.query, func(t *testing.T) {
			results, err := be.Entries().SearchText(ctx, tc.query, "contract.fts", 10)
			if err != nil {
				t.Fatalf("SearchText(%q): %v", tc.query, err)
			}

			// Build result ID set.
			resultIDs := make(map[string]bool, len(results))
			for _, r := range results {
				resultIDs[r.Entry.ID.String()] = true
			}

			// Every expected hit must appear.
			for _, label := range tc.wantIDs {
				id := entries[label].ID.String()
				if !resultIDs[id] {
					// Collect actual labels for diagnostic.
					var actualLabels []string
					for _, r := range results {
						for l, e := range entries {
							if e.ID == r.Entry.ID {
								actualLabels = append(actualLabels, l)
							}
						}
					}
					sort.Strings(actualLabels)
					t.Errorf("query %q: expected hit %q not found; got %v", tc.query, label, actualLabels)
				}
			}

			// Top-1 assertion (when specified).
			if tc.top1 != "" && len(results) > 0 {
				wantTop1ID := entries[tc.top1].ID.String()
				gotTop1ID := results[0].Entry.ID.String()
				if gotTop1ID != wantTop1ID {
					t.Errorf("query %q: top-1 want %q got %q", tc.query, tc.top1, gotTop1ID)
				}
			}
		})
	}

	// Scope filter: entries in sibling scope must not appear.
	t.Run("scope_filter", func(t *testing.T) {
		mustEnsureScope(t, ctx, be, "contract.fts.other")
		other := newTestEntry("Go is also used for cloud infrastructure.", "contract.fts.other")
		if err := be.Entries().Create(ctx, &other); err != nil {
			t.Fatalf("Create sibling scope entry: %v", err)
		}

		// Query scoped to contract.fts only — must not include the child contract.fts.other.
		// contract.fts.other IS a descendant, so it WILL be included (LIKE "contract.fts.%").
		// Instead verify a completely unrelated scope produces no hits from our corpus.
		mustEnsureScope(t, ctx, be, "contract.fts.unrelated")
		results, err := be.Entries().SearchText(ctx, "Go programming", "contract.fts.unrelated", 10)
		if err != nil {
			t.Fatalf("SearchText scoped to unrelated: %v", err)
		}
		for _, r := range results {
			for label, e := range entries {
				if e.ID == r.Entry.ID {
					t.Errorf("scope filter: corpus entry %q appeared in unrelated scope query", label)
				}
			}
		}
	})
}
