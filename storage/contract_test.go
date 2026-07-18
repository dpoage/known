// Package storage_test contains a contract test suite that validates all storage
// backends implement the same semantics. The suite runs against in-memory SQLite
// unconditionally and against PostgreSQL when KNOWN_INTEGRATION=1 (postgres leg
// emits t.Skip when the env var is absent so the absence is visible in output).
//
// Scope of coverage:
//   - EntryRepo: CRUD, upsert/dedup (ErrDuplicateContent), optimistic locking,
//     CreateOrUpdate, scope-descendant queries (ScopePrefix)
//   - EdgeRepo: create + list (EdgesFrom/EdgesTo)
//   - ScopeRepo: upsert + EnsureHierarchy
//   - SearchText parity on a fixed corpus (same docs, same queries → same hit
//     sets; top-1 constrained on multi-hit queries so a ranking inversion fails)
//   - SearchText KNOWN-DIVERGENT cases (stemming, diacritics, stopwords)
//
// # Known lexical divergences between SQLite FTS5 and Postgres tsvector
//
// The two backends use different tokenizers and therefore produce different hit
// sets for some inputs. These are DOCUMENTED divergences, not bugs to hide:
//
//	Stemming: Postgres 'english' config applies snowball stemming ("running"→"run");
//	SQLite FTS5 default unicode61 tokenizer does NOT stem. A query for "run" hits
//	documents containing "running" on Postgres but not on SQLite.
//
//	Diacritics: SQLite unicode61 folds diacritics by default ("café"→"cafe");
//	Postgres 'english' config has NO unaccent. A query for "cafe" hits the document
//	on SQLite but not on Postgres (unless pg_trgm+unaccent is added).
//
//	Stopwords: Postgres 'english' config removes common stopwords ("the", "a", etc.);
//	SQLite treats them as regular tokens. A query for "the" returns 0 results on
//	Postgres and may return many on SQLite.
//
// The KNOWN-DIVERGENT sub-tests below assert EACH backend's ACTUAL behaviour so
// that future normalization work has a clear red/green signal rather than silence.
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
	if os.Getenv("KNOWN_INTEGRATION") == "" {
		t.Skip("set KNOWN_INTEGRATION=1 to run postgres contract tests")
	}
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
	}{
		{"sqlite", newSQLiteBackend},
		{"postgres", newPostgresBackend},
	}

	for _, b := range backends {
		b := b
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()
			be := b.factory(t) // postgres factory calls t.Skip if not gated
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
			t.Run("SearchTextDivergent", func(t *testing.T) { contractSearchTextDivergent(t, ctx, be) })
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

// backendName returns "sqlite" or "postgres" for use in skip messages.
func backendName(be storage.Backend) string {
	switch be.(type) {
	case interface{ Pool() interface{} }:
		return "postgres"
	default:
		// Use type string as a heuristic.
		s := fmt.Sprintf("%T", be)
		if len(s) > 10 && s[:10] == "*postgres." {
			return "postgres"
		}
		return "sqlite"
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
	got1, err := be.Entries().Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get (reader 1): %v", err)
	}
	got2, err := be.Entries().Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get (reader 2): %v", err)
	}

	// First writer wins.
	got1.Content = "winner"
	got1.Touch()
	if err := be.Entries().Update(ctx, got1); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// Second writer must be rejected (stale version).
	got2.Content = "loser"
	got2.Touch()
	err = be.Entries().Update(ctx, got2)
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

// contractEntryScopeDescendant verifies List with ScopePrefix returns entries
// in the exact scope AND its descendants, but NOT sibling scopes whose name
// shares a prefix (e.g. "contract.treehouse" must not match prefix "contract.tree").
//
// Both backends use LIKE-prefix on the entries.scope TEXT column
// (scope = $1 OR scope LIKE $1||'.%'), NOT ltree — ltree only backs the scopes
// metadata table. So the trap is identical on both backends.
func contractEntryScopeDescendant(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.tree.child")
	mustEnsureScope(t, ctx, be, "contract.tree.other")
	mustEnsureScope(t, ctx, be, "contract.treehouse") // sibling — MUST NOT match prefix "contract.tree"

	eParent := newTestEntry("parent scope entry", "contract.tree")
	eChild := newTestEntry("child scope entry", "contract.tree.child")
	eOther := newTestEntry("other scope entry", "contract.tree.other")
	eSibling := newTestEntry("sibling scope entry", "contract.treehouse")

	for _, e := range []*model.Entry{&eParent, &eChild, &eOther, &eSibling} {
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

	// Positive: all descendants must appear.
	for _, want := range []*model.Entry{&eParent, &eChild, &eOther} {
		if !ids[want.ID.String()] {
			t.Errorf("scope descendant query missing entry %s (scope=%q)", want.ID, want.Scope)
		}
	}

	// Negative: sibling with matching text prefix but different dot-segment must NOT appear.
	if ids[eSibling.ID.String()] {
		t.Errorf("scope descendant query incorrectly included sibling %s (scope=%q)", eSibling.ID, eSibling.Scope)
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

// contractSearchText inserts a fixed corpus and verifies that both backends:
//   - return the expected hit SETS for each query.
//   - place the expected document at position 0 (top-1) for multi-hit queries,
//     so a ranking inversion on either backend causes a failure.
//   - return an error for an empty query.
//   - return nil, nil (not an error) for a no-hit query.
//
// Corpus design: queries use ASCII, non-stopword, non-stemmed terms that appear
// literally in both indexes (no diacritics, no inflected forms). This keeps the
// corpus "parity-friendly" — both backends return identical hit sets — while the
// top-1 assertions constrain ranking so an ordering inversion fails the suite.
// Tokenizer-level divergences (stemming, diacritics, stopwords) are covered
// separately in contractSearchTextDivergent.
//
// Score encoding note: SQLite BM25 scores are negative (more negative = more
// relevant); Postgres ts_rank scores are positive (higher = more relevant).
// Both are consistent within their backend. RRF in hybrid.go uses rank position
// only, so the sign difference is safe across backends. Stopword-only queries
// (e.g. "the") return nil on Postgres (english config strips stopwords) and may
// return results on SQLite — this is a known divergence, not an error.
func contractSearchText(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.fts")

	// Corpus: four documents. "programming" appears in alpha (title+content) and
	// gamma (content only), making it a real multi-hit query with a clear winner.
	corpus := []struct {
		label   string
		title   string
		content string
	}{
		// alpha: "programming" in BOTH title and content → ranks above gamma for query "programming".
		{"alpha", "Go programming language", "Go is a statically typed compiled programming language."},
		// beta: "scripting" only, no overlap with alpha/gamma on "programming".
		{"beta", "Python scripting", "Python is a dynamically typed interpreted language."},
		// gamma: "programming" in content only (systems context) → ranks below alpha for "programming".
		{"gamma", "Rust systems language", "Rust is a systems programming language focused on safety."},
		// delta: "database" and "index" — isolated terms, unambiguous winner for those queries.
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
		wantIDs []string // all of these must appear in results
		top1    string   // label of expected position-0 result (empty = unconstrained)
	}{
		{
			// "Go" + "programming": alpha has both terms in title AND content; gamma has
			// "programming" in content only and does not contain "Go" at all → alpha wins.
			query:   "Go programming",
			wantIDs: []string{"alpha"},
			top1:    "alpha",
		},
		{
			// "programming": alpha (title+content) vs gamma (content only).
			// Both backends weight title higher (SQLite FTS5 BM25 / Postgres weight 'A'),
			// so alpha must rank above gamma. This is a MULTI-HIT top-1 constraint —
			// inverting either backend's sort produces gamma at position 0 → test fails.
			query:   "programming",
			wantIDs: []string{"alpha", "gamma"},
			top1:    "alpha",
		},
		{
			// "database index": isolated to delta only.
			query:   "database index",
			wantIDs: []string{"delta"},
			top1:    "delta",
		},
		{
			// "language": alpha, beta, gamma all contain "language"; delta does not.
			// Top-1 unconstrained (all three documents use the word with similar
			// weight; exact ranking is scorer-dependent and not part of the contract).
			query:   "language",
			wantIDs: []string{"alpha", "beta", "gamma"},
			top1:    "",
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

			// Top-1 assertion (when specified — catches ranking inversions).
			if tc.top1 != "" && len(results) > 0 {
				wantTop1ID := entries[tc.top1].ID.String()
				gotTop1ID := results[0].Entry.ID.String()
				if gotTop1ID != wantTop1ID {
					var gotLabel string
					for l, e := range entries {
						if e.ID.String() == gotTop1ID {
							gotLabel = l
						}
					}
					t.Errorf("query %q: top-1 want %q got %q (%s)", tc.query, tc.top1, gotLabel, gotTop1ID)
				}
			}
		})
	}

	// Scope filter: a query against an unrelated sibling scope must not return
	// corpus entries from contract.fts.
	t.Run("scope_filter", func(t *testing.T) {
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

// ----------------------------------------------------------------------------
// SearchText KNOWN-DIVERGENT cases
// ----------------------------------------------------------------------------

// contractSearchTextDivergent documents three real tokenizer differences between
// the SQLite FTS5 (unicode61) and Postgres tsvector (english config) backends.
// Each sub-test asserts EACH backend's ACTUAL behaviour; the assertions are
// intentionally backend-specific so that future normalization work produces a
// clear red/green signal rather than leaving the gap invisible.
//
// Divergences:
//
//  1. Stemming: Postgres 'english' snowball stems "running"→"run"; SQLite unicode61
//     does NOT stem. Query "run" hits the doc on Postgres, misses on SQLite.
//
//  2. Diacritics: SQLite unicode61 folds diacritics ("café"→"cafe") by default;
//     Postgres 'english' has no unaccent normalization. Query "cafe" hits on SQLite,
//     misses on Postgres.
//
//  3. Stopwords: Postgres 'english' strips common stopwords ("the"); SQLite treats
//     them as ordinary tokens. Query "the" returns 0 on Postgres (nil,nil), returns
//     results on SQLite. NOTE: a stopword-only tsquery is invalid in Postgres and
//     plainto_tsquery returns a NULL tsquery → the @@ predicate never matches →
//     the result is nil,nil (not an error).
func contractSearchTextDivergent(t *testing.T, ctx context.Context, be storage.Backend) {
	mustEnsureScope(t, ctx, be, "contract.fts.divergent")

	// Determine backend type by probing the concrete type name.
	typeName := fmt.Sprintf("%T", be)
	isPostgres := len(typeName) >= 10 && typeName[:10] == "*postgres."

	// --- stemming ---
	t.Run("stemming", func(t *testing.T) {
		eRun := newTestEntry("The agent is running quickly.", "contract.fts.divergent")
		eRun.Title = "running entry"
		if err := be.Entries().Create(ctx, &eRun); err != nil {
			t.Fatalf("Create stemming doc: %v", err)
		}

		results, err := be.Entries().SearchText(ctx, "run", "contract.fts.divergent", 10)
		if err != nil {
			t.Fatalf("SearchText('run'): %v", err)
		}
		found := false
		for _, r := range results {
			if r.Entry.ID == eRun.ID {
				found = true
			}
		}

		if isPostgres {
			// Postgres snowball stems "running"→"run" → document IS found.
			if !found {
				t.Error("KNOWN-DIVERGENT(postgres/stemming): query 'run' expected to find doc containing 'running' (snowball stemmer), but did not")
			}
		} else {
			// SQLite unicode61 does NOT stem → document is NOT found.
			if found {
				t.Error("KNOWN-DIVERGENT(sqlite/stemming): query 'run' unexpectedly found doc containing 'running' (unicode61 has no stemming) — normalization may have been added")
			}
		}
	})

	// --- diacritics ---
	t.Run("diacritics", func(t *testing.T) {
		eCafe := newTestEntry("Visit the café for great coffee.", "contract.fts.divergent")
		eCafe.Title = "café entry"
		if err := be.Entries().Create(ctx, &eCafe); err != nil {
			t.Fatalf("Create diacritics doc: %v", err)
		}

		results, err := be.Entries().SearchText(ctx, "cafe", "contract.fts.divergent", 10)
		if err != nil {
			t.Fatalf("SearchText('cafe'): %v", err)
		}
		found := false
		for _, r := range results {
			if r.Entry.ID == eCafe.ID {
				found = true
			}
		}

		if isPostgres {
			// Postgres 'english' has no unaccent → "café" ≠ "cafe" → NOT found.
			if found {
				t.Error("KNOWN-DIVERGENT(postgres/diacritics): query 'cafe' unexpectedly found doc containing 'café' — unaccent normalization may have been added")
			}
		} else {
			// SQLite unicode61 folds diacritics → "café" matches "cafe" → IS found.
			if !found {
				t.Error("KNOWN-DIVERGENT(sqlite/diacritics): query 'cafe' expected to find doc containing 'café' (unicode61 diacritic folding), but did not")
			}
		}
	})

	// --- stopwords ---
	t.Run("stopwords", func(t *testing.T) {
		eThe := newTestEntry("The quick brown fox jumps.", "contract.fts.divergent")
		eThe.Title = "the entry"
		if err := be.Entries().Create(ctx, &eThe); err != nil {
			t.Fatalf("Create stopwords doc: %v", err)
		}

		results, err := be.Entries().SearchText(ctx, "the", "contract.fts.divergent", 10)

		if isPostgres {
			// Postgres 'english' strips "the" as a stopword → plainto_tsquery returns
			// NULL tsquery → @@ predicate never matches → nil, nil (not an error).
			if err != nil {
				t.Errorf("KNOWN-DIVERGENT(postgres/stopwords): query 'the' expected nil error (stopword → empty tsquery), got %v", err)
			}
			if len(results) != 0 {
				t.Errorf("KNOWN-DIVERGENT(postgres/stopwords): query 'the' expected 0 results, got %d", len(results))
			}
		} else {
			// SQLite treats "the" as a real token → document IS found (no error).
			if err != nil {
				t.Errorf("KNOWN-DIVERGENT(sqlite/stopwords): query 'the' unexpected error: %v", err)
			}
			found := false
			for _, r := range results {
				if r.Entry.ID == eThe.ID {
					found = true
				}
			}
			if !found {
				t.Error("KNOWN-DIVERGENT(sqlite/stopwords): query 'the' expected to find doc containing 'The', but did not")
			}
		}
	})
}
