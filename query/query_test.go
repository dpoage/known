package query

import (
	"context"
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
	entries map[string]*model.Entry
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

func (m *mockEmbedder) Dimensions() int  { return m.dims }
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

	var e2Score, e3Score float64
	for _, r := range results {
		if r.Entry.ID == e2.ID {
			e2Score = r.Score
		}
		if r.Entry.ID == e3.ID {
			e3Score = r.Score
		}
	}

	if e2Score != 0.9 {
		t.Errorf("strong edge result score = %f, want 0.9", e2Score)
	}
	if e3Score != 0.3 {
		t.Errorf("weak edge result score = %f, want 0.3", e3Score)
	}
	if e2Score <= e3Score {
		t.Errorf("strong edge score (%f) should be greater than weak edge score (%f)", e2Score, e3Score)
	}
}

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

	for _, r := range results {
		if r.Entry.ID == e2.ID {
			if r.Score != 1.0 {
				t.Errorf("nil weight expansion score = %f, want 1.0", r.Score)
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
		{0.0, 1.0, 1.0},       // exact match
		{1.0, 0.49, 0.51},     // unit distance
		{9.0, 0.09, 0.11},     // far away
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
