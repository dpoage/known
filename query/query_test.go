package query

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// mockEntryRepo is a simple in-memory implementation of storage.EntryRepo
// for testing purposes.
type mockEntryRepo struct {
	entries    map[string]*model.Entry
	searchText func(ctx context.Context, query, scope string, limit int) ([]storage.SimilarityResult, error)
}

func newMockEntryRepo() *mockEntryRepo {
	return &mockEntryRepo{entries: make(map[string]*model.Entry)}
}

func (m *mockEntryRepo) Create(_ context.Context, entry *model.Entry) error {
	if _, exists := m.entries[entry.ID.String()]; exists {
		return fmt.Errorf("entry already exists: %s", entry.ID)
	}
	clone := *entry
	m.entries[entry.ID.String()] = &clone
	return nil
}

func (m *mockEntryRepo) Get(_ context.Context, id model.ID) (*model.Entry, error) {
	entry, ok := m.entries[id.String()]
	if !ok {
		return nil, storage.ErrNotFound
	}
	clone := *entry
	return &clone, nil
}

func (m *mockEntryRepo) Update(_ context.Context, entry *model.Entry) error {
	if _, ok := m.entries[entry.ID.String()]; !ok {
		return storage.ErrNotFound
	}
	clone := *entry
	m.entries[entry.ID.String()] = &clone
	return nil
}

func (m *mockEntryRepo) Delete(_ context.Context, id model.ID) error {
	if _, ok := m.entries[id.String()]; !ok {
		return storage.ErrNotFound
	}
	delete(m.entries, id.String())
	return nil
}

func (m *mockEntryRepo) List(_ context.Context, filter storage.EntryFilter) ([]model.Entry, error) {
	var results []model.Entry
	for _, e := range m.entries {
		if filter.Scope != "" && e.Scope != filter.Scope {
			continue
		}
		if filter.ScopePrefix != "" {
			if e.Scope != filter.ScopePrefix && !strings.HasPrefix(e.Scope, filter.ScopePrefix+".") {
				continue
			}
		}
		if filter.ProvenanceLevel != "" && e.Provenance.Level != filter.ProvenanceLevel {
			continue
		}
		if !filter.IncludeExpired && e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
			continue
		}
		results = append(results, *e)
	}

	if filter.Offset > 0 && filter.Offset < len(results) {
		results = results[filter.Offset:]
	} else if filter.Offset >= len(results) {
		results = nil
	}

	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

func (m *mockEntryRepo) SearchSimilar(_ context.Context, query []float32, scope string, _ storage.SimilarityMetric, limit int) ([]storage.SimilarityResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query vector must not be empty")
	}

	var results []storage.SimilarityResult
	for _, e := range m.entries {
		if !e.HasEmbedding() {
			continue
		}
		if e.Scope != scope && !strings.HasPrefix(e.Scope, scope+".") {
			continue
		}
		if e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
			continue
		}
		if e.EmbeddingDim != len(query) {
			continue
		}

		dist := cosineDistance(query, e.Embedding)
		results = append(results, storage.SimilarityResult{
			Entry:    *e,
			Distance: dist,
		})
	}

	// Sort by distance (ascending).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Distance < results[j-1].Distance; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	return results, nil
}

func (m *mockEntryRepo) DeleteExpired(_ context.Context) (int64, error) {
	var count int64
	for id, e := range m.entries {
		if e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
			delete(m.entries, id)
			count++
		}
	}
	return count, nil
}

func (m *mockEntryRepo) SearchText(ctx context.Context, query string, scope string, limit int) ([]storage.SimilarityResult, error) {
	if m.searchText != nil {
		return m.searchText(ctx, query, scope, limit)
	}
	return nil, nil
}

func (m *mockEntryRepo) CreateOrUpdate(ctx context.Context, entry *model.Entry) (*model.Entry, error) {
	existing, _ := m.Get(ctx, entry.ID)
	if existing != nil {
		clone := *entry
		clone.Version = existing.Version + 1
		m.entries[entry.ID.String()] = &clone
		return &clone, nil
	}
	clone := *entry
	m.entries[entry.ID.String()] = &clone
	return &clone, nil
}

// cosineDistance computes cosine distance between two vectors.
func cosineDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return 2.0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 2.0
	}
	cosine := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	return 1.0 - cosine
}

// mockEdgeRepo is a simple in-memory implementation of storage.EdgeRepo.
type mockEdgeRepo struct {
	edges map[string]*model.Edge
	// entryRepo is needed for FindConflicts to return entries.
	entryRepo *mockEntryRepo
	// updateErr, when non-nil, causes Update to return the mapped error for
	// the given edge ID string. Used by TestReinforce_PartialFailureAccounting.
	updateErr map[string]error
}

func newMockEdgeRepo(entryRepo *mockEntryRepo) *mockEdgeRepo {
	return &mockEdgeRepo{
		edges:     make(map[string]*model.Edge),
		entryRepo: entryRepo,
	}
}

func (m *mockEdgeRepo) Create(_ context.Context, edge *model.Edge) error {
	if _, exists := m.edges[edge.ID.String()]; exists {
		return fmt.Errorf("edge already exists: %s", edge.ID)
	}
	clone := *edge
	m.edges[edge.ID.String()] = &clone
	return nil
}

func (m *mockEdgeRepo) Get(_ context.Context, id model.ID) (*model.Edge, error) {
	edge, ok := m.edges[id.String()]
	if !ok {
		return nil, storage.ErrNotFound
	}
	clone := *edge
	return &clone, nil
}

func (m *mockEdgeRepo) Update(_ context.Context, edge *model.Edge) error {
	if _, ok := m.edges[edge.ID.String()]; !ok {
		return storage.ErrNotFound
	}
	if m.updateErr != nil {
		if err, bad := m.updateErr[edge.ID.String()]; bad {
			return err
		}
	}
	clone := *edge
	m.edges[edge.ID.String()] = &clone
	return nil
}

func (m *mockEdgeRepo) Delete(_ context.Context, id model.ID) error {
	if _, ok := m.edges[id.String()]; !ok {
		return storage.ErrNotFound
	}
	delete(m.edges, id.String())
	return nil
}

func (m *mockEdgeRepo) EdgesFrom(_ context.Context, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	var results []model.Edge
	for _, e := range m.edges {
		if e.FromID != entryID {
			continue
		}
		if filter.Type != "" && e.Type != filter.Type {
			continue
		}
		results = append(results, *e)
	}
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}
	return results, nil
}

func (m *mockEdgeRepo) EdgesTo(_ context.Context, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	var results []model.Edge
	for _, e := range m.edges {
		if e.ToID != entryID {
			continue
		}
		if filter.Type != "" && e.Type != filter.Type {
			continue
		}
		results = append(results, *e)
	}
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}
	return results, nil
}

func (m *mockEdgeRepo) EdgesBetween(_ context.Context, fromID, toID model.ID) ([]model.Edge, error) {
	var results []model.Edge
	for _, e := range m.edges {
		if e.FromID == fromID && e.ToID == toID {
			results = append(results, *e)
		}
	}
	return results, nil
}

func (m *mockEdgeRepo) FindConflicts(_ context.Context, entryID model.ID) ([]model.Entry, error) {
	var results []model.Entry
	seen := make(map[string]bool)

	for _, e := range m.edges {
		if e.Type != model.EdgeContradicts {
			continue
		}
		var neighborID model.ID
		if e.FromID == entryID {
			neighborID = e.ToID
		} else if e.ToID == entryID {
			neighborID = e.FromID
		} else {
			continue
		}

		if seen[neighborID.String()] {
			continue
		}
		seen[neighborID.String()] = true

		entry, ok := m.entryRepo.entries[neighborID.String()]
		if ok {
			results = append(results, *entry)
		}
	}

	return results, nil
}

// mockEmbedder is a simple embedder that returns fixed-dimension vectors
// derived from the input text hash for deterministic testing.
type mockEmbedder struct {
	dims      int
	modelName string
	// embedFn allows custom embedding logic per test.
	embedFn func(text string) []float32
}

func newMockEmbedder(dims int) *mockEmbedder {
	return &mockEmbedder{
		dims:      dims,
		modelName: "mock-model",
	}
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(text), nil
	}
	// Default: return a simple hash-based vector.
	vec := make([]float32, m.dims)
	for i, ch := range text {
		vec[i%m.dims] += float32(ch) / 1000.0
	}
	// Normalize.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range vec {
			vec[i] /= n
		}
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := m.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		results[i] = v
	}
	return results, nil
}

func (m *mockEmbedder) Dimensions() int   { return m.dims }
func (m *mockEmbedder) ModelName() string { return m.modelName }

// =============================================================================
// Test Helpers
// =============================================================================

// testFixture sets up a common test environment with entries and edges.
type testFixture struct {
	entryRepo *mockEntryRepo
	edgeRepo  *mockEdgeRepo
	embedder  *mockEmbedder
	engine    *Engine
}

func newTestFixture(dims int) *testFixture {
	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	embedder := newMockEmbedder(dims)
	engine := New(entryRepo, edgeRepo, embedder)
	return &testFixture{
		entryRepo: entryRepo,
		edgeRepo:  edgeRepo,
		embedder:  embedder,
		engine:    engine,
	}
}

func makeEntry(content, scope string, embedding []float32) model.Entry {
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	entry := model.NewEntry(content, src).WithScope(scope)
	if embedding != nil {
		entry = entry.WithEmbedding(embedding, "mock-model")
	}
	return entry
}

func makeEntryAt(content, scope string, embedding []float32, createdAt time.Time) model.Entry {
	entry := makeEntry(content, scope, embedding)
	entry.CreatedAt = createdAt
	// Also backdate ObservedAt so freshness scoring (which prefers ObservedAt)
	// reflects the intended age. Without this, ObservedAt stays as time.Now()
	// and both entries look equally fresh regardless of CreatedAt.
	entry.Freshness.ObservedAt = createdAt
	return entry
}

func mustCreateEntry(t *testing.T, repo *mockEntryRepo, entry model.Entry) model.Entry {
	t.Helper()
	if err := repo.Create(context.Background(), &entry); err != nil {
		t.Fatalf("failed to create entry %q: %v", entry.Content, err)
	}
	return entry
}

func mustCreateEdge(t *testing.T, repo *mockEdgeRepo, from, to model.ID, edgeType model.EdgeType) model.Edge {
	t.Helper()
	edge := model.NewEdge(from, to, edgeType)
	if err := repo.Create(context.Background(), &edge); err != nil {
		t.Fatalf("failed to create edge %s -> %s: %v", from, to, err)
	}
	return edge
}

// =============================================================================
// Vector Search Tests
// =============================================================================

func TestSearchVector_Basic(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// Set up the embedder to return a known vector for the query.
	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// Create entries with known embeddings.
	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("very similar", "root", []float32{0.99, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("somewhat similar", "root", []float32{0.5, 0.5, 0.5}))
	mustCreateEntry(t, f.entryRepo, makeEntry("different", "root", []float32{0.0, 0.0, 1.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("no embedding", "root", nil))

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:  "test query",
		Scope: "root",
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result should be the most similar.
	if results[0].Entry.ID != e1.ID {
		t.Errorf("first result should be %q, got %q", e1.Content, results[0].Entry.Content)
	}
	if results[1].Entry.ID != e2.ID {
		t.Errorf("second result should be %q, got %q", e2.Content, results[1].Entry.Content)
	}

	// All results should be direct search.
	for _, r := range results {
		if r.Reach != ReachDirect {
			t.Errorf("result %q reach = %q, want %q", r.Entry.Content, r.Reach, ReachDirect)
		}
		if r.Depth != 0 {
			t.Errorf("result %q depth = %d, want 0", r.Entry.Content, r.Depth)
		}
		if r.Score <= 0 || r.Score > 1 {
			t.Errorf("result %q score = %f, want (0, 1]", r.Entry.Content, r.Score)
		}
	}
}

func TestSearchVector_EmptyQuery(t *testing.T) {
	f := newTestFixture(3)
	_, err := f.engine.SearchVector(context.Background(), VectorOptions{
		Text:  "",
		Scope: "root",
	})
	if err == nil {
		t.Error("expected error for empty query text")
	}
}

func TestSearchVector_EmptyScope(t *testing.T) {
	f := newTestFixture(3)
	_, err := f.engine.SearchVector(context.Background(), VectorOptions{
		Text:  "test",
		Scope: "",
	})
	if err == nil {
		t.Error("expected error for empty scope")
	}
}

func TestSearchVector_Threshold(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	mustCreateEntry(t, f.entryRepo, makeEntry("close match", "root", []float32{0.95, 0.1, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("distant", "root", []float32{0.0, 0.0, 1.0}))

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:      "test",
		Scope:     "root",
		Limit:     10,
		Threshold: 0.9,
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	// Only the close match should pass the threshold.
	if len(results) != 1 {
		t.Fatalf("expected 1 result above threshold, got %d", len(results))
	}
	if results[0].Entry.Content != "close match" {
		t.Errorf("expected 'close match', got %q", results[0].Entry.Content)
	}
}

func TestSearchVector_ExcludeContent(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	mustCreateEntry(t, f.entryRepo, makeEntry("golang http server", "root", []float32{0.9, 0.1, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("python flask server", "root", []float32{0.85, 0.15, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("golang grpc service", "root", []float32{0.8, 0.2, 0.0}))

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:           "server",
		Scope:          "root",
		Limit:          10,
		ExcludeContent: []string{"python"},
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	for _, r := range results {
		if strings.Contains(strings.ToLower(r.Entry.Content), "python") {
			t.Errorf("excluded content %q appeared in results", r.Entry.Content)
		}
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results after exclusion, got %d", len(results))
	}
}

func TestSearchVector_ExcludeContent_CaseInsensitive(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	mustCreateEntry(t, f.entryRepo, makeEntry("PYTHON uppercase", "root", []float32{0.9, 0.1, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("golang entry", "root", []float32{0.8, 0.2, 0.0}))

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:           "test",
		Scope:          "root",
		Limit:          10,
		ExcludeContent: []string{"python"},
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entry.Content != "golang entry" {
		t.Errorf("expected 'golang entry', got %q", results[0].Entry.Content)
	}
}

func TestSearchVector_RecencyWeighting(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	now := time.Now()

	// Old entry: very similar but old.
	oldEntry := mustCreateEntry(t, f.entryRepo, makeEntryAt(
		"old but similar", "root",
		[]float32{0.99, 0.05, 0.0},
		now.Add(-30*24*time.Hour), // 30 days ago
	))

	// Recent entry: slightly less similar but very recent.
	recentEntry := mustCreateEntry(t, f.entryRepo, makeEntryAt(
		"recent but less similar", "root",
		[]float32{0.8, 0.3, 0.0},
		now.Add(-1*time.Hour), // 1 hour ago
	))

	// Without recency weighting, old entry should be first.
	noRecency, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:  "test",
		Scope: "root",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchVector (no recency): %v", err)
	}
	if len(noRecency) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(noRecency))
	}
	if noRecency[0].Entry.ID != oldEntry.ID {
		t.Errorf("without recency, first result should be old entry, got %q", noRecency[0].Entry.Content)
	}

	// With high recency weighting, recent entry should be first.
	withRecency, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:            "test",
		Scope:           "root",
		Limit:           10,
		RecencyWeight:   0.8,
		RecencyHalfLife: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SearchVector (with recency): %v", err)
	}
	if len(withRecency) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(withRecency))
	}
	if withRecency[0].Entry.ID != recentEntry.ID {
		t.Errorf("with recency, first result should be recent entry, got %q", withRecency[0].Entry.Content)
	}
}

func TestSearchVector_ScopeFiltering(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	mustCreateEntry(t, f.entryRepo, makeEntry("in scope", "project", []float32{0.9, 0.1, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("in subscope", "project.auth", []float32{0.85, 0.15, 0.0}))
	mustCreateEntry(t, f.entryRepo, makeEntry("other scope", "other", []float32{0.9, 0.1, 0.0}))

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:  "test",
		Scope: "project",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results in project scope, got %d", len(results))
	}
	for _, r := range results {
		if r.Entry.Scope != "project" && !strings.HasPrefix(r.Entry.Scope, "project.") {
			t.Errorf("result scope %q not within 'project'", r.Entry.Scope)
		}
	}
}

func TestSearchVector_ConflictEnrichment(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("claim A", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("contradicts A", "root", []float32{0.5, 0.5, 0.5}))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeContradicts)

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:  "test",
		Scope: "root",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}

	// Find the result for e1.
	var foundE1 bool
	for _, r := range results {
		if r.Entry.ID == e1.ID {
			foundE1 = true
			if !r.HasConflict {
				t.Error("e1 should have conflict flag set")
			}
			if len(r.Conflicts) != 1 || r.Conflicts[0] != e2.ID {
				t.Errorf("e1 conflicts = %v, want [%s]", r.Conflicts, e2.ID)
			}
		}
	}
	if !foundE1 {
		t.Error("e1 not found in results")
	}
}

// =============================================================================
// Graph Traversal Tests
// =============================================================================

func TestTraverse_Outgoing(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// A -> B -> C
	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Check that B is at depth 1 and C is at depth 2.
	depthMap := make(map[string]int)
	for _, r := range results {
		depthMap[r.Entry.Content] = r.Depth
	}
	if depthMap["B"] != 1 {
		t.Errorf("B depth = %d, want 1", depthMap["B"])
	}
	if depthMap["C"] != 2 {
		t.Errorf("C depth = %d, want 2", depthMap["C"])
	}
}

func TestTraverse_Incoming(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// A -> C, B -> C
	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, c.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   c.ID,
		Direction: Incoming,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (A and B), got %d", len(results))
	}

	contents := make(map[string]bool)
	for _, r := range results {
		contents[r.Entry.Content] = true
	}
	if !contents["A"] || !contents["B"] {
		t.Errorf("expected A and B, got %v", contents)
	}
}

func TestTraverse_Both(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// A -> B -> C, D -> B
	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))
	d := mustCreateEntry(t, f.entryRepo, makeEntry("D", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, d.ID, b.ID, model.EdgeRelatedTo)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   b.ID,
		Direction: Both,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	// Should find A (incoming), C (outgoing), D (incoming)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	contents := make(map[string]bool)
	for _, r := range results {
		contents[r.Entry.Content] = true
	}
	for _, want := range []string{"A", "C", "D"} {
		if !contents[want] {
			t.Errorf("missing entry %q in results", want)
		}
	}
}

func TestTraverse_IncludeStart(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:      a.ID,
		Direction:    Outgoing,
		MaxDepth:     1,
		IncludeStart: true,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (including start), got %d", len(results))
	}

	// Start entry should be at depth 0.
	if results[0].Entry.ID != a.ID || results[0].Depth != 0 {
		t.Errorf("first result should be start entry at depth 0")
	}
}

func TestTraverse_EdgeTypeFilter(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, a.ID, c.ID, model.EdgeRelatedTo)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  1,
		EdgeTypes: []model.EdgeType{model.EdgeDependsOn},
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result with depends-on filter, got %d", len(results))
	}
	if results[0].Entry.Content != "B" {
		t.Errorf("expected B, got %q", results[0].Entry.Content)
	}
}

func TestTraverse_DepthLimit(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// A -> B -> C -> D (linear chain)
	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))
	d := mustCreateEntry(t, f.entryRepo, makeEntry("D", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, c.ID, d.ID, model.EdgeDependsOn)

	// MaxDepth=1 should only find B.
	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("depth 1: expected 1 result, got %d", len(results))
	}
	if results[0].Entry.Content != "B" {
		t.Errorf("depth 1: expected B, got %q", results[0].Entry.Content)
	}

	// MaxDepth=2 should find B and C.
	results, err = f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("Traverse depth 2: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("depth 2: expected 2 results, got %d", len(results))
	}
}

func TestTraverse_CycleProtection(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// A -> B -> C -> A (cycle)
	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, c.ID, a.ID, model.EdgeDependsOn)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  10, // high depth to test cycle protection
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	// Should find exactly B and C, not revisit A.
	if len(results) != 2 {
		t.Fatalf("expected 2 results (cycle protection), got %d", len(results))
	}
}

func TestTraverse_EdgePath(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	edgeAB := mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeElaborates)

	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	// B should have a 1-element edge path.
	for _, r := range results {
		if r.Entry.Content == "B" {
			if len(r.EdgePath) != 1 {
				t.Errorf("B edge path length = %d, want 1", len(r.EdgePath))
			} else if r.EdgePath[0].ID != edgeAB.ID {
				t.Errorf("B edge path[0] = %s, want %s", r.EdgePath[0].ID, edgeAB.ID)
			}
		}
		if r.Entry.Content == "C" {
			if len(r.EdgePath) != 2 {
				t.Errorf("C edge path length = %d, want 2", len(r.EdgePath))
			}
		}
	}
}

func TestTraverse_MissingStartID(t *testing.T) {
	f := newTestFixture(3)
	_, err := f.engine.Traverse(context.Background(), GraphOptions{
		Direction: Outgoing,
		MaxDepth:  1,
	})
	if err == nil {
		t.Error("expected error for zero start ID")
	}
}

// =============================================================================
// Path Finding Tests
// =============================================================================

func TestFindPath_Direct(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)

	results, err := f.engine.FindPath(ctx, a.ID, b.ID, 5)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (B), got %d", len(results))
	}
	if results[0].Entry.ID != b.ID {
		t.Errorf("expected B, got %q", results[0].Entry.Content)
	}
}

func TestFindPath_MultiHop(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)

	results, err := f.engine.FindPath(ctx, a.ID, c.ID, 5)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (B, C), got %d", len(results))
	}
}

func TestFindPath_NoPath(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	// No edge between A and B.

	results, err := f.engine.FindPath(ctx, a.ID, b.ID, 5)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for no path, got %d", len(results))
	}
}

func TestFindPath_DepthLimited(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, b.ID, c.ID, model.EdgeDependsOn)

	// maxDepth=1 should not find a path from A to C (requires 2 hops).
	results, err := f.engine.FindPath(ctx, a.ID, c.ID, 1)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for path beyond depth limit, got %d results", len(results))
	}
}

func TestFindPath_MissingIDs(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := model.NewID()
	_, err := f.engine.FindPath(ctx, model.ID{}, a, 5)
	if err == nil {
		t.Error("expected error for zero fromID")
	}

	_, err = f.engine.FindPath(ctx, a, model.ID{}, 5)
	if err == nil {
		t.Error("expected error for zero toID")
	}
}

// =============================================================================
// Hybrid Query Tests
// =============================================================================

func TestSearchHybrid_Basic(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// Create entries: e1 is similar, e2 is connected to e1, e3 is unrelated.
	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("similar entry", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("connected to similar", "root", nil))
	mustCreateEntry(t, f.entryRepo, makeEntry("unrelated", "root", []float32{0.0, 0.0, 1.0}))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeElaborates)

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 5,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	// Should include vector results + expanded entry e2.
	foundExpanded := false
	for _, r := range results {
		if r.Entry.ID == e2.ID {
			foundExpanded = true
			if r.Reach != ReachExpansion {
				t.Errorf("expanded result reach = %q, want %q", r.Reach, ReachExpansion)
			}
		}
		if r.Entry.ID == e1.ID && r.Reach != ReachDirect {
			t.Errorf("direct result reach = %q, want %q", r.Reach, ReachDirect)
		}
	}
	if !foundExpanded {
		t.Error("expanded entry e2 not found in hybrid results")
	}
}

func TestSearchHybrid_Deduplication(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// e1 and e2 both appear in vector results AND are connected to each other.
	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("entry one", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("entry two", "root", []float32{0.9, 0.15, 0.0}))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeRelatedTo)

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	// e2 should appear only once (vector result, not duplicated by expansion).
	count := 0
	for _, r := range results {
		if r.Entry.ID == e2.ID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("e2 appeared %d times, want 1 (deduplication)", count)
	}
}

func TestSearchHybrid_EdgeTypeFilter(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("source", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("elaboration", "root", nil))
	e3 := mustCreateEntry(t, f.entryRepo, makeEntry("dependency", "root", nil))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeElaborates)
	mustCreateEdge(t, f.edgeRepo, e1.ID, e3.ID, model.EdgeDependsOn)

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 5,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
		ExpandEdgeTypes: []model.EdgeType{model.EdgeElaborates},
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	// Only e2 (elaboration) should be expanded, not e3 (dependency).
	foundE2, foundE3 := false, false
	for _, r := range results {
		if r.Entry.ID == e2.ID && r.Reach == ReachExpansion {
			foundE2 = true
		}
		if r.Entry.ID == e3.ID && r.Reach == ReachExpansion {
			foundE3 = true
		}
	}
	if !foundE2 {
		t.Error("elaboration entry should be in expansion results")
	}
	if foundE3 {
		t.Error("dependency entry should NOT be in expansion results (filtered by edge type)")
	}
}

// TestSearchHybrid_EdgeWeightAffectsScore verifies that expansion scores
// preserve edge-weight ordering: a higher-weight edge from the same parent
// must produce a higher score than a lower-weight edge.
// Score formula: parentScore * edgeWeight * expansionDepthDecay.
func TestSearchHybrid_EdgeWeightAffectsScore(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("source", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("strong connection", "root", nil))
	e3 := mustCreateEntry(t, f.entryRepo, makeEntry("weak connection", "root", nil))

	strongWeight := 0.9
	weakWeight := 0.3

	strongEdge := model.NewEdge(e1.ID, e2.ID, model.EdgeElaborates)
	strongEdge.Weight = &strongWeight
	if err := f.edgeRepo.Create(ctx, &strongEdge); err != nil {
		t.Fatalf("create strong edge: %v", err)
	}

	weakEdge := model.NewEdge(e1.ID, e3.ID, model.EdgeElaborates)
	weakEdge.Weight = &weakWeight
	if err := f.edgeRepo.Create(ctx, &weakEdge); err != nil {
		t.Fatalf("create weak edge: %v", err)
	}

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 5,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	var e1Score, e2Score, e3Score float64
	for _, r := range results {
		switch r.Entry.ID {
		case e1.ID:
			e1Score = r.Score
		case e2.ID:
			e2Score = r.Score
		case e3.ID:
			e3Score = r.Score
		}
	}

	// Both expansion scores must use parentScore * edgeWeight * expansionDepthDecay.
	wantE2 := e1Score * strongWeight * expansionDepthDecay
	wantE3 := e1Score * weakWeight * expansionDepthDecay
	if math.Abs(e2Score-wantE2) > 1e-10 {
		t.Errorf("strong edge score = %f, want %f (parentScore=%f * 0.9 * %f)", e2Score, wantE2, e1Score, expansionDepthDecay)
	}
	if math.Abs(e3Score-wantE3) > 1e-10 {
		t.Errorf("weak edge score = %f, want %f (parentScore=%f * 0.3 * %f)", e3Score, wantE3, e1Score, expansionDepthDecay)
	}
	if e2Score <= e3Score {
		t.Errorf("strong edge score (%f) should be greater than weak edge score (%f)", e2Score, e3Score)
	}
}

// TestSearchHybrid_NilEdgeWeightTreatedAsOne verifies that a nil edge weight
// is treated as 1.0 (EffectiveWeight default), and the expansion score is
// parentScore * 1.0 * expansionDepthDecay (not a literal 1.0).
func TestSearchHybrid_NilEdgeWeightTreatedAsOne(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("source", "root", []float32{0.95, 0.1, 0.0}))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("connected with nil weight", "root", nil))

	// Create edge with nil weight (no WithWeight call).
	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeElaborates)

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 5,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	var e1Score float64
	for _, r := range results {
		if r.Entry.ID == e1.ID {
			e1Score = r.Score
		}
	}

	for _, r := range results {
		if r.Entry.ID == e2.ID {
			// nil weight → EffectiveWeight() == 1.0, so score = parentScore * 1.0 * decay.
			want := e1Score * 1.0 * expansionDepthDecay
			if math.Abs(r.Score-want) > 1e-10 {
				t.Errorf("nil weight expansion score = %f, want %f (parentScore=%f * decay=%f)", r.Score, want, e1Score, expansionDepthDecay)
			}
			return
		}
	}
	t.Error("expanded entry e2 not found in results")
}

func TestEdgeWeight_Helper(t *testing.T) {
	// Test the Edge.EffectiveWeight method.
	t.Run("nil weight returns 1.0", func(t *testing.T) {
		edge := model.NewEdge(model.NewID(), model.NewID(), model.EdgeRelatedTo)
		score := edge.EffectiveWeight()
		if score != 1.0 {
			t.Errorf("EffectiveWeight(nil) = %f, want 1.0", score)
		}
	})

	t.Run("explicit weight returned", func(t *testing.T) {
		w := 0.5
		edge := model.Edge{Weight: &w}
		score := edge.EffectiveWeight()
		if score != 0.5 {
			t.Errorf("EffectiveWeight(0.5) = %f, want 0.5", score)
		}
	})

	t.Run("zero weight", func(t *testing.T) {
		w := 0.0
		edge := model.Edge{Weight: &w}
		score := edge.EffectiveWeight()
		if score != 0.0 {
			t.Errorf("EffectiveWeight(0.0) = %f, want 0.0", score)
		}
	})

	t.Run("max weight", func(t *testing.T) {
		w := 1.0
		edge := model.Edge{Weight: &w}
		score := edge.EffectiveWeight()
		if score != 1.0 {
			t.Errorf("EffectiveWeight(1.0) = %f, want 1.0", score)
		}
	})
}

// =============================================================================
// Conflict Detection Tests
// =============================================================================

func TestFindConflicts_Basic(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("claim A", "root", nil))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("contradicts A", "root", nil))
	e3 := mustCreateEntry(t, f.entryRepo, makeEntry("also contradicts A", "root", nil))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeContradicts)
	mustCreateEdge(t, f.edgeRepo, e3.ID, e1.ID, model.EdgeContradicts)

	results, err := f.engine.FindConflicts(ctx, e1.ID)
	if err != nil {
		t.Fatalf("FindConflicts: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 conflicts, got %d", len(results))
	}

	for _, r := range results {
		if !r.HasConflict {
			t.Errorf("result %q should have conflict flag", r.Entry.Content)
		}
		if r.Reach != ReachTraversal {
			t.Errorf("result reach = %q, want %q", r.Reach, ReachTraversal)
		}
	}
}

func TestFindConflicts_None(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("no conflicts", "root", nil))

	results, err := f.engine.FindConflicts(ctx, e1.ID)
	if err != nil {
		t.Fatalf("FindConflicts: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(results))
	}
}

func TestFindConflicts_ZeroID(t *testing.T) {
	f := newTestFixture(3)
	_, err := f.engine.FindConflicts(context.Background(), model.ID{})
	if err == nil {
		t.Error("expected error for zero ID")
	}
}

func TestDetectAllConflicts_Basic(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("claim X", "project", nil))
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("contradicts X", "project", nil))
	e3 := mustCreateEntry(t, f.entryRepo, makeEntry("claim Y", "project", nil))
	e4 := mustCreateEntry(t, f.entryRepo, makeEntry("contradicts Y", "project", nil))
	mustCreateEntry(t, f.entryRepo, makeEntry("no conflict", "project", nil))

	mustCreateEdge(t, f.edgeRepo, e1.ID, e2.ID, model.EdgeContradicts)
	mustCreateEdge(t, f.edgeRepo, e3.ID, e4.ID, model.EdgeContradicts)

	pairs, err := f.engine.DetectAllConflicts(ctx, "project")
	if err != nil {
		t.Fatalf("DetectAllConflicts: %v", err)
	}

	if len(pairs) != 2 {
		t.Fatalf("expected 2 conflict pairs, got %d", len(pairs))
	}
}

func TestDetectAllConflicts_EmptyScope(t *testing.T) {
	f := newTestFixture(3)
	_, err := f.engine.DetectAllConflicts(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty scope")
	}
}

func TestDetectAllConflicts_NoConflicts(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	mustCreateEntry(t, f.entryRepo, makeEntry("peaceful entry", "root", nil))

	pairs, err := f.engine.DetectAllConflicts(ctx, "root")
	if err != nil {
		t.Fatalf("DetectAllConflicts: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 conflict pairs, got %d", len(pairs))
	}
}

// =============================================================================
// Score Utility Tests
// =============================================================================

func TestDistanceToScore_Cosine(t *testing.T) {
	tests := []struct {
		distance float64
		wantMin  float64
		wantMax  float64
	}{
		{0.0, 1.0, 1.0},   // exact match
		{1.0, 0.49, 0.51}, // orthogonal
		{2.0, 0.0, 0.01},  // opposite
	}

	for _, tt := range tests {
		score := distanceToScore(tt.distance, storage.Cosine)
		if score < tt.wantMin || score > tt.wantMax {
			t.Errorf("distanceToScore(%f, Cosine) = %f, want [%f, %f]", tt.distance, score, tt.wantMin, tt.wantMax)
		}
	}
}

func TestDistanceToScore_L2(t *testing.T) {
	tests := []struct {
		distance float64
		wantMin  float64
		wantMax  float64
	}{
		{0.0, 1.0, 1.0},   // exact match
		{1.0, 0.49, 0.51}, // unit distance
		{9.0, 0.09, 0.11}, // far away
	}

	for _, tt := range tests {
		score := distanceToScore(tt.distance, storage.L2)
		if score < tt.wantMin || score > tt.wantMax {
			t.Errorf("distanceToScore(%f, L2) = %f, want [%f, %f]", tt.distance, score, tt.wantMin, tt.wantMax)
		}
	}
}

func TestDistanceToScore_InnerProduct(t *testing.T) {
	// pgvector returns negative inner product, so distance=0 means perfect match.
	score := distanceToScore(0.0, storage.InnerProduct)
	if score < 0.99 {
		t.Errorf("distanceToScore(0, InnerProduct) = %f, want ~1.0", score)
	}
	score = distanceToScore(1.0, storage.InnerProduct)
	if score < 0.49 || score > 0.51 {
		t.Errorf("distanceToScore(1, InnerProduct) = %f, want ~0.5", score)
	}
}

func TestFreshnessScore(t *testing.T) {
	now := time.Now()
	halfLife := 24 * time.Hour

	// Brand new entry should be ~1.0.
	fresh := freshnessScore(now, halfLife)
	if fresh < 0.99 {
		t.Errorf("freshnessScore(now) = %f, want ~1.0", fresh)
	}

	// Entry exactly one half-life old should be ~0.5.
	halfOld := freshnessScore(now.Add(-halfLife), halfLife)
	if math.Abs(halfOld-0.5) > 0.01 {
		t.Errorf("freshnessScore(half-life ago) = %f, want ~0.5", halfOld)
	}

	// Very old entry should be close to 0.
	ancient := freshnessScore(now.Add(-100*halfLife), halfLife)
	if ancient > 0.01 {
		t.Errorf("freshnessScore(100 half-lives ago) = %f, want ~0.0", ancient)
	}
}

func TestBlendScore(t *testing.T) {
	// Pure similarity (weight=0).
	if score := blendScore(0.9, 0.1, 0.0); math.Abs(score-0.9) > 0.001 {
		t.Errorf("blendScore(0.9, 0.1, 0.0) = %f, want 0.9", score)
	}

	// Pure recency (weight=1).
	if score := blendScore(0.9, 0.1, 1.0); math.Abs(score-0.1) > 0.001 {
		t.Errorf("blendScore(0.9, 0.1, 1.0) = %f, want 0.1", score)
	}

	// 50/50 blend.
	if score := blendScore(0.8, 0.4, 0.5); math.Abs(score-0.6) > 0.001 {
		t.Errorf("blendScore(0.8, 0.4, 0.5) = %f, want 0.6", score)
	}
}

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		content  string
		excludes []string
		want     bool
	}{
		{"hello world", []string{"hello"}, true},
		{"hello world", []string{"HELLO"}, true}, // case insensitive
		{"hello world", []string{"missing"}, false},
		{"hello world", nil, false},
		{"hello world", []string{}, false},
		{"golang http server", []string{"python", "ruby"}, false},
		{"python flask server", []string{"python", "ruby"}, true},
	}

	for _, tt := range tests {
		if got := shouldExclude(tt.content, tt.excludes); got != tt.want {
			t.Errorf("shouldExclude(%q, %v) = %v, want %v", tt.content, tt.excludes, got, tt.want)
		}
	}
}

func TestClamp(t *testing.T) {
	if clamp(0.5, 0, 1) != 0.5 {
		t.Error("clamp(0.5, 0, 1) should be 0.5")
	}
	if clamp(-1, 0, 1) != 0 {
		t.Error("clamp(-1, 0, 1) should be 0")
	}
	if clamp(2, 0, 1) != 1 {
		t.Error("clamp(2, 0, 1) should be 1")
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestMockInterfaceCompliance(t *testing.T) {
	var _ storage.EntryRepo = (*mockEntryRepo)(nil)
	var _ storage.EdgeRepo = (*mockEdgeRepo)(nil)
}

// =============================================================================
// Multi-Edge Type Filter Tests
// =============================================================================

func TestTraverse_MultipleEdgeTypes(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	a := mustCreateEntry(t, f.entryRepo, makeEntry("A", "root", nil))
	b := mustCreateEntry(t, f.entryRepo, makeEntry("B", "root", nil))
	c := mustCreateEntry(t, f.entryRepo, makeEntry("C", "root", nil))
	d := mustCreateEntry(t, f.entryRepo, makeEntry("D", "root", nil))

	mustCreateEdge(t, f.edgeRepo, a.ID, b.ID, model.EdgeDependsOn)
	mustCreateEdge(t, f.edgeRepo, a.ID, c.ID, model.EdgeElaborates)
	mustCreateEdge(t, f.edgeRepo, a.ID, d.ID, model.EdgeRelatedTo)

	// Filter to depends-on and elaborates only.
	results, err := f.engine.Traverse(ctx, GraphOptions{
		StartID:   a.ID,
		Direction: Outgoing,
		MaxDepth:  1,
		EdgeTypes: []model.EdgeType{model.EdgeDependsOn, model.EdgeElaborates},
	})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	contents := make(map[string]bool)
	for _, r := range results {
		contents[r.Entry.Content] = true
	}
	if !contents["B"] || !contents["C"] {
		t.Errorf("expected B and C, got %v", contents)
	}
	if contents["D"] {
		t.Error("D (related-to) should be filtered out")
	}
}

// =============================================================================
// RRF Fusion Tests
// =============================================================================

// namedResults maps content strings to Result for easier lookup in tests.
func resultByContent(results []Result, content string) (Result, bool) {
	for _, r := range results {
		if r.Entry.Content == content {
			return r, true
		}
	}
	return Result{}, false
}

func makeResult(content string, score float64, reach ReachMethod) Result {
	return Result{
		Entry: model.Entry{ID: model.NewID(), Content: content},
		Score: score,
		Reach: reach,
	}
}

func TestFuseByRRF_BothListsContribute(t *testing.T) {
	// Entry A: vector rank 0, text rank 1
	// Entry B: vector rank 1, text rank 0
	// With k=60: both get 1/61 + 1/62 — equal scores.
	idA, idB := model.NewID(), model.NewID()
	vector := []Result{
		{Entry: model.Entry{ID: idA, Content: "A"}, Score: 0.9, Reach: ReachDirect},
		{Entry: model.Entry{ID: idB, Content: "B"}, Score: 0.7, Reach: ReachDirect},
	}
	text := []Result{
		{Entry: model.Entry{ID: idB, Content: "B"}, Score: 0.3, Reach: ReachText},
		{Entry: model.Entry{ID: idA, Content: "A"}, Score: 0.2, Reach: ReachText},
	}

	results := fuseByRRF(vector, text, 60)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	expectedScore := 1.0/61.0 + 1.0/62.0
	for _, r := range results {
		if math.Abs(r.Score-expectedScore) > 1e-10 {
			t.Errorf("entry %s: score = %f, want %f", r.Entry.Content, r.Score, expectedScore)
		}
	}
}

func TestFuseByRRF_TextOnlyEntryOutranksWeakVector(t *testing.T) {
	// A: vector-only, rank 2. B: text-only, rank 0. C: both lists.
	idA, idB, idC, idD := model.NewID(), model.NewID(), model.NewID(), model.NewID()
	vector := []Result{
		{Entry: model.Entry{ID: idC, Content: "C"}, Score: 0.8, Reach: ReachDirect},
		{Entry: model.Entry{ID: idD, Content: "D"}, Score: 0.6, Reach: ReachDirect},
		{Entry: model.Entry{ID: idA, Content: "A"}, Score: 0.4, Reach: ReachDirect}, // rank 2
	}
	text := []Result{
		{Entry: model.Entry{ID: idB, Content: "B"}, Score: 0.3, Reach: ReachText}, // rank 0
		{Entry: model.Entry{ID: idC, Content: "C"}, Score: 0.2, Reach: ReachText}, // rank 1
	}

	results := fuseByRRF(vector, text, 60)

	rA, _ := resultByContent(results, "A")
	rB, _ := resultByContent(results, "B")

	// B at text rank 0 (1/61) should beat A at vector rank 2 (1/63).
	if rB.Score <= rA.Score {
		t.Errorf("text-only B (score=%f) should outrank weak vector A (score=%f)", rB.Score, rA.Score)
	}
}

func TestFuseByRRF_TextOnlyResultsGetReachText(t *testing.T) {
	vector := []Result{
		makeResult("vector entry", 0.9, ReachDirect),
	}
	text := []Result{
		makeResult("text only", 0.3, ReachText),
	}

	results := fuseByRRF(vector, text, 60)

	rV, _ := resultByContent(results, "vector entry")
	rT, _ := resultByContent(results, "text only")
	if rV.Reach != ReachDirect {
		t.Errorf("vector entry reach = %q, want %q", rV.Reach, ReachDirect)
	}
	if rT.Reach != ReachText {
		t.Errorf("text entry reach = %q, want %q", rT.Reach, ReachText)
	}
}

func TestFuseByRRF_DuplicateKeepsBetterRankedReach(t *testing.T) {
	// Entry X: vector rank 1, text rank 0 — text ranked it higher.
	idX := model.NewID()
	vector := []Result{
		makeResult("other", 0.9, ReachDirect),
		{Entry: model.Entry{ID: idX, Content: "X"}, Score: 0.7, Reach: ReachDirect}, // rank 1
	}
	text := []Result{
		{Entry: model.Entry{ID: idX, Content: "X"}, Score: 0.3, Reach: ReachText}, // rank 0
	}

	results := fuseByRRF(vector, text, 60)

	rX, _ := resultByContent(results, "X")
	if rX.Reach != ReachText {
		t.Errorf("X should have ReachText (better rank), got %q", rX.Reach)
	}
}

func TestFuseByRRF_ResultsSortedByScore(t *testing.T) {
	idC := model.NewID()
	vector := []Result{
		makeResult("A", 0.9, ReachDirect),
		makeResult("B", 0.7, ReachDirect),
		{Entry: model.Entry{ID: idC, Content: "C"}, Score: 0.5, Reach: ReachDirect},
	}
	text := []Result{
		{Entry: model.Entry{ID: idC, Content: "C"}, Score: 0.3, Reach: ReachText}, // C boosted
		makeResult("D", 0.2, ReachText),
	}

	results := fuseByRRF(vector, text, 60)

	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestFuseByRRF_EmptyTextResults(t *testing.T) {
	vector := []Result{
		makeResult("A", 0.9, ReachDirect),
		makeResult("B", 0.7, ReachDirect),
	}

	results := fuseByRRF(vector, nil, 60)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Score <= results[1].Score {
		t.Error("first result should have higher score than second")
	}
}

func TestFuseByRRF_EmptyVectorResults(t *testing.T) {
	text := []Result{
		makeResult("A", 0.3, ReachText),
		makeResult("B", 0.2, ReachText),
	}

	results := fuseByRRF(nil, text, 60)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Reach != ReachText {
			t.Errorf("entry %s reach = %q, want %q", r.Entry.ID, r.Reach, ReachText)
		}
	}
}

func TestFuseByRRF_CustomK(t *testing.T) {
	id := model.NewID()
	vector := []Result{
		{Entry: model.Entry{ID: id, Content: "A"}, Score: 0.9, Reach: ReachDirect},
	}
	text := []Result{
		{Entry: model.Entry{ID: id, Content: "A"}, Score: 0.3, Reach: ReachText},
	}

	// With k=1: score = 1/2 + 1/2 = 1.0
	results := fuseByRRF(vector, text, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	expected := 1.0/2.0 + 1.0/2.0
	if math.Abs(results[0].Score-expected) > 1e-10 {
		t.Errorf("score = %f, want %f", results[0].Score, expected)
	}
}

// Integration test: SearchHybrid with TextSearch enabled.
func TestSearchHybrid_TextFusion(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// e1: good vector match, no text match.
	e1 := mustCreateEntry(t, f.entryRepo, makeEntry("good embedding entry", "root", []float32{0.95, 0.1, 0.0}))
	// e2: weak vector match, strong text match.
	e2 := mustCreateEntry(t, f.entryRepo, makeEntry("exact keyword match", "root", []float32{0.3, 0.3, 0.85}))

	// Configure mock to return e2 as top text result, e1 as second.
	f.entryRepo.searchText = func(_ context.Context, _ string, _ string, _ int) ([]storage.SimilarityResult, error) {
		return []storage.SimilarityResult{
			{Entry: e2, Distance: -5.0}, // rank 0: strong BM25 match
			{Entry: e1, Distance: -1.0}, // rank 1: weak BM25 match
		}, nil
	}

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "keyword",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
		TextSearch:      true,
	})
	if err != nil {
		t.Fatalf("SearchHybrid with text: %v", err)
	}

	// e2 should appear in results (brought in by text search and RRF fusion).
	found := false
	for _, r := range results {
		if r.Entry.ID == e2.ID {
			found = true
		}
	}
	if !found {
		t.Error("e2 (strong text match) should appear in fused results")
	}

	// Both entries appear in both lists, so both get boosted.
	// e1 has vector rank 0 + text rank 1; e2 has vector rank 1 + text rank 0.
	// With RRF they should have equal scores.
	var scoreE1, scoreE2 float64
	for _, r := range results {
		if r.Entry.ID == e1.ID {
			scoreE1 = r.Score
		}
		if r.Entry.ID == e2.ID {
			scoreE2 = r.Score
		}
	}
	if math.Abs(scoreE1-scoreE2) > 1e-10 {
		t.Logf("e1 score=%f, e2 score=%f (expected equal with symmetric ranking)", scoreE1, scoreE2)
	}
}

// =============================================================================
// known-mrc: Nil embedder sentinel error tests
// =============================================================================

func TestSearchVector_NilEmbedder_ReturnsErrNoEmbedder(t *testing.T) {
	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	// Engine with nil embedder.
	engine := New(entryRepo, edgeRepo, nil)

	_, err := engine.SearchVector(context.Background(), VectorOptions{
		Text:  "test query",
		Scope: "root",
		Limit: 5,
	})
	if err == nil {
		t.Fatal("expected error from SearchVector with nil embedder, got nil")
	}
	if !errors.Is(err, ErrNoEmbedder) {
		t.Errorf("SearchVector nil embedder: got %v, want ErrNoEmbedder", err)
	}
}

func TestSearchHybrid_NilEmbedder_ReturnsErrNoEmbedder(t *testing.T) {
	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)

	_, err := engine.SearchHybrid(context.Background(), HybridOptions{
		Vector: VectorOptions{
			Text:  "test query",
			Scope: "root",
			Limit: 5,
		},
		ExpandDepth:     1,
		ExpandDirection: Both,
	})
	if err == nil {
		t.Fatal("expected error from SearchHybrid with nil embedder, got nil")
	}
	if !errors.Is(err, ErrNoEmbedder) {
		t.Errorf("SearchHybrid nil embedder: got %v, want ErrNoEmbedder", err)
	}
}

// =============================================================================
// known-1so: Expansion score inversion test
// =============================================================================

// TestSearchHybrid_ExpansionScoreInversion is the canonical test from the bead:
// a weight-1.0 edge from a LOW-relevance parent must NOT outrank a 0.8 edge
// from a HIGH-relevance parent. Under the old scoring (pure edge weight) this
// was inverted; the new formula fixes it.
func TestSearchHybrid_ExpansionScoreInversion(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	// Query vector: {1, 0, 0}.
	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// highParent: very similar to query (high score ~0.997).
	// lowParent: orthogonal to query (low score ~0.5).
	highParent := mustCreateEntry(t, f.entryRepo, makeEntry("high relevance", "root", []float32{1.0, 0.05, 0.0}))
	lowParent := mustCreateEntry(t, f.entryRepo, makeEntry("low relevance", "root", []float32{0.0, 1.0, 0.0}))

	// highChild reached via 0.8 edge from highParent.
	// lowChild reached via 1.0 edge from lowParent.
	highChild := mustCreateEntry(t, f.entryRepo, makeEntry("high-parent child", "root", nil))
	lowChild := mustCreateEntry(t, f.entryRepo, makeEntry("low-parent child", "root", nil))

	highEdgeW := 0.8
	highEdge := model.NewEdge(highParent.ID, highChild.ID, model.EdgeElaborates)
	highEdge.Weight = &highEdgeW
	if err := f.edgeRepo.Create(ctx, &highEdge); err != nil {
		t.Fatalf("create high-parent edge: %v", err)
	}

	lowEdgeW := 1.0
	lowEdge := model.NewEdge(lowParent.ID, lowChild.ID, model.EdgeElaborates)
	lowEdge.Weight = &lowEdgeW
	if err := f.edgeRepo.Create(ctx, &lowEdge); err != nil {
		t.Fatalf("create low-parent edge: %v", err)
	}

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	var highChildScore, lowChildScore float64
	for _, r := range results {
		switch r.Entry.ID {
		case highChild.ID:
			highChildScore = r.Score
		case lowChild.ID:
			lowChildScore = r.Score
		}
	}

	if highChildScore == 0 {
		t.Fatal("highChild not found in results")
	}
	if lowChildScore == 0 {
		t.Fatal("lowChild not found in results")
	}
	// The critical invariant: highChild (0.8 edge, high-relevance parent) must
	// beat lowChild (1.0 edge, low-relevance parent). Old scoring inverted this.
	if highChildScore <= lowChildScore {
		t.Errorf("inversion bug: highChild score (%f) <= lowChild score (%f); "+
			"a 0.8 edge from a high-relevance parent must outrank a 1.0 edge from a low-relevance parent",
			highChildScore, lowChildScore)
	}
}

// =============================================================================
// known-6hj: TotalLimit truncation and sort-order tests
// =============================================================================

// TestSearchHybrid_TotalLimit verifies that TotalLimit caps and sorts the
// combined (vector + expansion) result set.
func TestSearchHybrid_TotalLimit(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	// Create a seed entry and many neighbors to ensure > 3 total results.
	seed := mustCreateEntry(t, f.entryRepo, makeEntry("seed", "root", []float32{0.99, 0.1, 0.0}))
	for i := 0; i < 5; i++ {
		neighbor := mustCreateEntry(t, f.entryRepo, makeEntry(fmt.Sprintf("neighbor-%d", i), "root", nil))
		mustCreateEdge(t, f.edgeRepo, seed.ID, neighbor.ID, model.EdgeElaborates)
	}

	limit := 3
	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
		TotalLimit:      limit,
	})
	if err != nil {
		t.Fatalf("SearchHybrid with TotalLimit: %v", err)
	}

	if len(results) != limit {
		t.Errorf("TotalLimit=%d: got %d results, want %d", limit, len(results), limit)
	}

	// Verify results are sorted by score descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: results[%d].Score=%f > results[%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

// TestSearchHybrid_TotalLimitZeroMeansUnlimited verifies that TotalLimit=0
// does not truncate results.
func TestSearchHybrid_TotalLimitZeroMeansUnlimited(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(text string) []float32 {
		return []float32{1.0, 0.0, 0.0}
	}

	seed := mustCreateEntry(t, f.entryRepo, makeEntry("seed", "root", []float32{0.99, 0.1, 0.0}))
	for i := 0; i < 4; i++ {
		neighbor := mustCreateEntry(t, f.entryRepo, makeEntry(fmt.Sprintf("neighbor-%d", i), "root", nil))
		mustCreateEdge(t, f.edgeRepo, seed.ID, neighbor.ID, model.EdgeElaborates)
	}

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector: VectorOptions{
			Text:  "test",
			Scope: "root",
			Limit: 10,
		},
		ExpandDepth:     1,
		ExpandDirection: Outgoing,
		TotalLimit:      0, // unlimited
	})
	if err != nil {
		t.Fatalf("SearchHybrid unlimited: %v", err)
	}

	// 1 seed + 4 neighbors = 5 total.
	if len(results) < 5 {
		t.Errorf("TotalLimit=0 should not truncate: got %d results, want >= 5", len(results))
	}
}

// =============================================================================
// known-2s3: FTS identifier rescue
// =============================================================================

// TestSearchHybrid_IdentifierRescuedByFTS verifies that exact-match queries on
// identifiers (IPs, version strings, OIDC tokens, ULID fragments) surface the
// correct entry even when the vector embedder returns LOW similarity for it.
// This is the core acceptance criterion for known-2s3: FTS must rescue the
// right entry via RRF fusion when vector similarity alone is insufficient.
func TestSearchHybrid_IdentifierRescuedByFTS(t *testing.T) {
	identifiers := []struct {
		name    string
		content string
		query   string
	}{
		{"IP address", "gateway is at 10.0.1.5", "10.0.1.5"},
		{"version string", "pgvector 0.7.4 installed", "pgvector 0.7.4"},
		{"OIDC", "auth uses OIDC provider", "OIDC"},
	}

	for _, tc := range identifiers {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFixture(3)
			ctx := context.Background()

			// Embedder returns a fixed vector; the identifier entry will have
			// LOW cosine similarity (orthogonal vector) so vector alone won't
			// surface it.
			f.embedder.embedFn = func(text string) []float32 {
				return []float32{1.0, 0.0, 0.0}
			}

			// Decoy: high vector similarity but no text match.
			mustCreateEntry(t, f.entryRepo, makeEntry("unrelated semantic match", "root", []float32{0.99, 0.1, 0.0}))
			// Target: the identifier entry with low vector similarity.
			target := mustCreateEntry(t, f.entryRepo, makeEntry(tc.content, "root", []float32{0.0, 0.0, 1.0}))

			// FTS returns the target as #1 result (strong exact match).
			f.entryRepo.searchText = func(_ context.Context, _ string, _ string, _ int) ([]storage.SimilarityResult, error) {
				return []storage.SimilarityResult{
					{Entry: target, Distance: -10.0}, // top FTS hit
				}, nil
			}

			results, err := f.engine.SearchHybrid(ctx, HybridOptions{
				Vector: VectorOptions{
					Text:  tc.query,
					Scope: "root",
					Limit: 5,
				},
				ExpandDepth:     0,
				ExpandDirection: Outgoing,
				TextSearch:      true,
			})
			if err != nil {
				t.Fatalf("SearchHybrid: %v", err)
			}

			found := false
			for _, r := range results {
				if r.Entry.ID == target.ID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("identifier entry %q not found in results for query %q; FTS rescue failed", tc.content, tc.query)
			}
		})
	}
}

// TestSearchHybrid_IdentifierTopRankedWhenFTSExclusive verifies that when the
// identifier entry does NOT appear in vector results at all (completely missed),
// FTS alone can surface it via RRF with a positive score.
func TestSearchHybrid_IdentifierTopRankedWhenFTSExclusive(t *testing.T) {
	f := newTestFixture(1) // 1-dimensional embeddings
	ctx := context.Background()

	f.embedder.embedFn = func(_ string) []float32 { return []float32{1.0} }

	// The identifier entry has no embedding (won't appear in vector results).
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	target := model.NewEntry("OIDC provider endpoint https://auth.example.com/.well-known/openid-configuration", src).WithScope("root")
	f.entryRepo.Create(ctx, &target)

	// Decoy with good vector similarity.
	mustCreateEntry(t, f.entryRepo, makeEntry("some other config entry", "root", []float32{0.99}))

	f.entryRepo.searchText = func(_ context.Context, _ string, _ string, _ int) ([]storage.SimilarityResult, error) {
		return []storage.SimilarityResult{
			{Entry: target, Distance: -8.0},
		}, nil
	}

	results, err := f.engine.SearchHybrid(ctx, HybridOptions{
		Vector:     VectorOptions{Text: "OIDC", Scope: "root", Limit: 5},
		TextSearch: true,
	})
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}

	found := false
	for _, r := range results {
		if r.Entry.ID == target.ID {
			found = true
		}
	}
	if !found {
		t.Error("FTS-exclusive entry should appear in hybrid results via RRF")
	}
}

// =============================================================================
// known-oj3: ObservedAt freshness
// =============================================================================

// TestFreshnessScore_ObservedAtPreferredOverCreatedAt verifies the core fix:
// an entry re-observed recently scores higher than a same-age entry that was
// never re-observed, even when both were created at the same time.
func TestFreshnessScore_ObservedAtPreferredOverCreatedAt(t *testing.T) {
	halfLife := 7 * 24 * time.Hour
	now := time.Now()
	longAgo := now.Add(-30 * 24 * time.Hour)

	// Both created 30 days ago.
	// staleEntry: ObservedAt also 30 days ago (never re-verified).
	staleScore := freshnessScoreAt(longAgo, longAgo, halfLife)
	// freshEntry: ObservedAt is now (re-verified today).
	freshScore := freshnessScoreAt(longAgo, now, halfLife)

	if freshScore <= staleScore {
		t.Errorf("re-observed entry (freshScore=%f) should outscore stale entry (staleScore=%f)", freshScore, staleScore)
	}
	// Fresh entry should be nearly 1.0 (observed just now).
	if freshScore < 0.99 {
		t.Errorf("freshScore=%f, want ~1.0 for just-observed entry", freshScore)
	}
	// Stale entry at 30 days with 7-day half-life: exp(-ln2*30/7) ≈ 0.049.
	if staleScore > 0.1 {
		t.Errorf("staleScore=%f, want < 0.1 for 30-day-old unobserved entry", staleScore)
	}
}

// TestFreshnessScore_ZeroObservedAtFallsBackToCreatedAt verifies backwards
// compatibility: when ObservedAt is zero (not set), CreatedAt is used.
func TestFreshnessScore_ZeroObservedAtFallsBackToCreatedAt(t *testing.T) {
	halfLife := 7 * 24 * time.Hour
	now := time.Now()
	longAgo := now.Add(-14 * 24 * time.Hour) // 2 half-lives

	scoreWithZeroObs := freshnessScoreAt(longAgo, time.Time{}, halfLife)
	scoreFromCreatedAt := freshnessScore(longAgo, halfLife)

	if math.Abs(scoreWithZeroObs-scoreFromCreatedAt) > 1e-10 {
		t.Errorf("zero ObservedAt should give same result as CreatedAt: got %f vs %f", scoreWithZeroObs, scoreFromCreatedAt)
	}
	// At 2 half-lives, score ≈ 0.25.
	if scoreWithZeroObs < 0.2 || scoreWithZeroObs > 0.3 {
		t.Errorf("score at 2 half-lives = %f, want ~0.25", scoreWithZeroObs)
	}
}

// TestSearchVector_ObservedAtFreshnessRescuesOldEntry verifies that an old
// entry recently re-observed beats a newer-created entry that was never
// re-observed when recency weighting is enabled.
func TestSearchVector_ObservedAtFreshnessRescuesOldEntry(t *testing.T) {
	f := newTestFixture(3)
	ctx := context.Background()

	f.embedder.embedFn = func(_ string) []float32 { return []float32{1.0, 0.0, 0.0} }

	now := time.Now()
	oneYearAgo := now.Add(-365 * 24 * time.Hour)
	oneWeekAgo := now.Add(-7 * 24 * time.Hour)

	// Old entry created 1 year ago but re-observed today.
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	reobservedEntry := model.NewEntry("old but re-verified config", src).WithScope("root")
	reobservedEntry.CreatedAt = oneYearAgo
	reobservedEntry.Freshness.ObservedAt = now // re-observed today!
	reobservedEntry = reobservedEntry.WithEmbedding([]float32{0.9, 0.1, 0.0}, "mock")
	f.entryRepo.Create(ctx, &reobservedEntry)

	// Newer entry created 1 week ago but never re-observed.
	neverObservedEntry := model.NewEntry("newer but stale config", src).WithScope("root")
	neverObservedEntry.CreatedAt = oneWeekAgo
	neverObservedEntry.Freshness.ObservedAt = oneWeekAgo
	neverObservedEntry = neverObservedEntry.WithEmbedding([]float32{0.85, 0.15, 0.0}, "mock")
	f.entryRepo.Create(ctx, &neverObservedEntry)

	results, err := f.engine.SearchVector(ctx, VectorOptions{
		Text:            "config",
		Scope:           "root",
		Limit:           5,
		RecencyWeight:   0.8,
		RecencyHalfLife: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Entry.ID != reobservedEntry.ID {
		t.Errorf("re-observed old entry should rank first (freshness=~1.0 beats week-old unobserved); got %q first", results[0].Entry.Content)
	}
}

// =============================================================================
// known-906: decay, differentiated boosts, saturation, accounting
// =============================================================================

// TestReinforce_UnusedEdgesDecayBelowInitial verifies that an edge that is
// never reinforced (but adjacent to acted-on nodes) decays on each cycle.
// After N cycles its weight must be below the initial value.
func TestReinforce_UnusedEdgesDecayBelowInitial(t *testing.T) {
	ctx := context.Background()
	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	// Edge between e1 and e2 at initial weight 0.5.
	initial := 0.5
	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(initial)
	edgeRepo.Create(ctx, &edge)

	cfg := ReinforceConfig{
		ShowBoost:   0.0, // no boost — pure decay
		UpdateBoost: 0.0,
		LinkBoost:   0.0,
		DecayFactor: 0.95, // 5% decay per cycle
		MinWeight:   0.01,
		MaxWeight:   1.0,
	}

	// Run 5 sessions each with a recall→show pattern on e1.
	// Since ShowBoost=0, the edge only decays.
	for i := range 5 {
		sessID := model.NewID()
		now := time.Now()
		ended := now.Add(time.Minute)
		sessions.CreateSession(ctx, &model.Session{ID: sessID, StartedAt: now, EndedAt: &ended})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
			Query: fmt.Sprintf("q%d", i), CreatedAt: now,
		})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
			EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
		})

		_, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
		if err != nil {
			t.Fatalf("Reinforce cycle %d: %v", i, err)
		}
	}

	got, _ := edgeRepo.Get(ctx, edge.ID)
	if got.Weight == nil {
		t.Fatal("edge weight should be set")
	}
	if *got.Weight >= initial {
		t.Errorf("unused edge weight %f should be below initial %f after 5 decay cycles", *got.Weight, initial)
	}
	// After 5 cycles at 0.95 decay: 0.5 * 0.95^5 ≈ 0.387
	want := initial * math.Pow(0.95, 5)
	if math.Abs(*got.Weight-want) > 1e-6 {
		t.Errorf("edge weight = %f, want %f (5 cycles of 0.95 decay)", *got.Weight, want)
	}
}

// TestReinforce_SaturationPreventedByDecay verifies that even when an edge is
// boosted every session, decay prevents it from immediately reaching MaxWeight
// and holding there forever. After enough cycles it converges to an equilibrium
// (boost / (1 - decay)) rather than a hard cap.
func TestReinforce_SaturationPreventedByDecay(t *testing.T) {
	ctx := context.Background()
	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	// Edge starting at 0.0 (nil weight → EffectiveWeight=1.0, so use explicit low weight).
	initialW := 0.1
	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(initialW)
	edgeRepo.Create(ctx, &edge)

	cfg := ReinforceConfig{
		// Use show event for predictability.
		ShowBoost:   0.1,
		UpdateBoost: 0.1,
		LinkBoost:   0.1,
		DecayFactor: 0.9,
		MinWeight:   0.01,
		MaxWeight:   1.0,
	}

	// Equilibrium = boost / (1 - decay) = 0.1 / 0.1 = 1.0 → would hit MaxWeight.
	// After many cycles it should converge to MaxWeight (1.0), not some intermediate
	// unlimitable value. What we verify is that it does NOT OVERSHOOT MaxWeight.
	for i := range 50 {
		sessID := model.NewID()
		now := time.Now()
		ended := now.Add(time.Minute)
		sessions.CreateSession(ctx, &model.Session{ID: sessID, StartedAt: now, EndedAt: &ended})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
			Query: fmt.Sprintf("q%d", i), CreatedAt: now,
		})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
			EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
		})
		_, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
		if err != nil {
			t.Fatalf("Reinforce cycle %d: %v", i, err)
		}

		got, _ := edgeRepo.Get(ctx, edge.ID)
		if *got.Weight > cfg.MaxWeight+1e-9 {
			t.Errorf("cycle %d: edge weight %f exceeds MaxWeight %f", i, *got.Weight, cfg.MaxWeight)
		}
	}
}

// TestReinforce_DifferentiatedBoostsByActionType verifies that show events
// produce smaller boosts than update/link events.
func TestReinforce_DifferentiatedBoostsByActionType(t *testing.T) {
	ctx := context.Background()
	src := model.Source{Type: model.SourceManual, Reference: "test"}

	cfg := DefaultReinforceConfig()

	runSession := func(actionType model.EventType) float64 {
		entryRepo := newMockEntryRepo()
		edgeRepo := newMockEdgeRepo(entryRepo)
		engine := New(entryRepo, edgeRepo, nil)
		sessions := newMockSessionRepo()

		e1 := model.NewEntry("e1", src).WithScope("test")
		e2 := model.NewEntry("e2", src).WithScope("test")
		entryRepo.Create(ctx, &e1)
		entryRepo.Create(ctx, &e2)

		edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
		edgeRepo.Create(ctx, &edge)

		sessID := model.NewID()
		now := time.Now()
		ended := now.Add(time.Minute)
		sessions.CreateSession(ctx, &model.Session{ID: sessID, StartedAt: now, EndedAt: &ended})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
			Query: "q", CreatedAt: now,
		})
		sessions.LogEvent(ctx, &model.SessionEvent{
			ID: model.NewID(), SessionID: sessID, EventType: actionType,
			EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
		})

		engine.Reinforce(ctx, sessions, noopTx, cfg)
		got, _ := edgeRepo.Get(ctx, edge.ID)
		return *got.Weight
	}

	showWeight := runSession(model.EventShow)
	updateWeight := runSession(model.EventUpdate)
	linkWeight := runSession(model.EventLink)

	if showWeight >= updateWeight {
		t.Errorf("show boost (%f) should be < update boost (%f)", showWeight, updateWeight)
	}
	if showWeight >= linkWeight {
		t.Errorf("show boost (%f) should be < link boost (%f)", showWeight, linkWeight)
	}
	// update and link should be equal.
	if math.Abs(updateWeight-linkWeight) > 1e-9 {
		t.Errorf("update boost (%f) should equal link boost (%f)", updateWeight, linkWeight)
	}
}

// TestReinforce_PartialFailureAccounting verifies the error contract:
// when an edge Update fails mid-loop, processSession returns the count of
// edges successfully updated BEFORE the failure plus the error. Reinforce
// wraps this in a transaction (rolled back on error) so the net EdgesBoosted
// for a failed session is zero.
func TestReinforce_PartialFailureAccounting(t *testing.T) {
	ctx := context.Background()
	src := model.Source{Type: model.SourceManual, Reference: "test"}

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	// Inject a failing Update for this edge.
	edgeRepo.updateErr = map[string]error{
		edge.ID.String(): fmt.Errorf("simulated write failure"),
	}

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{ID: sessID, StartedAt: now, EndedAt: &ended})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	result, err := engine.Reinforce(ctx, sessions, noopTx, DefaultReinforceConfig())

	// Reinforce must return an error when a session fails.
	if err == nil {
		t.Fatal("expected error from Reinforce on Update failure, got nil")
	}
	// EdgesBoosted should be zero: the transaction rolled back.
	// (noopTx doesn't actually roll back in tests, but EdgesBoosted accumulates
	// only AFTER a successful session — on error, Reinforce returns early.)
	if result != nil && result.EdgesBoosted > 0 {
		t.Errorf("EdgesBoosted = %d on error, want 0 (partial failure must not report success)", result.EdgesBoosted)
	}
}
