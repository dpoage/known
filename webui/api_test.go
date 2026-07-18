package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/sqlite"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// testFixture holds the seeded entries/edges for assertions in test bodies.
type testFixture struct {
	server *Server

	// entries, indexed by mnemonic name.
	schema  model.Entry // root scope, label "convention"
	ulids   model.Entry // root scope
	oauth   model.Entry // root.sub scope, label "security"
	refresh model.Entry // root.sub scope
	legacy  model.Entry // root.sub scope
	orphan  model.Entry // root scope, no edges

	// edges, indexed by mnemonic name.
	dependsOn   model.Edge // schema -[depends-on]-> ulids
	contradicts model.Edge // schema -[contradicts]-> legacy
	supersedes  model.Edge // oauth -[supersedes]-> refresh
	elaborates  model.Edge // oauth -[elaborates]-> schema (explicit weight 0.9)
	relatedTo   model.Edge // refresh -[related-to]-> ulids
	dangling    model.Edge // schema -[related-to]-> (deleted entry)
}

// newTestFixture builds an in-memory sqlite backend, seeds it, and returns a
// ready-to-use Server plus the seeded entities for assertions.
func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	ctx := context.Background()

	db, err := sqlite.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	entries := db.Entries()
	edges := db.Edges()
	scopesRepo := db.Scopes()

	if err := scopesRepo.EnsureHierarchy(ctx, "root.sub"); err != nil {
		t.Fatalf("ensure hierarchy: %v", err)
	}

	mustCreate := func(content, scope string, labels []string) model.Entry {
		t.Helper()
		e := model.NewEntry(content, model.Source{Type: model.SourceManual, Reference: "test"})
		e.Title = content
		e.Scope = scope
		e.Labels = labels
		result, err := entries.CreateOrUpdate(ctx, &e)
		if err != nil {
			t.Fatalf("create entry %q: %v", content, err)
		}
		return *result
	}

	f := &testFixture{}
	f.schema = mustCreate("Schema conventions", model.RootScope, []string{"convention"})
	f.ulids = mustCreate("All new tables use ULIDs", model.RootScope, nil)
	f.oauth = mustCreate("OAuth flow details", "root.sub", []string{"security"})
	f.refresh = mustCreate("Token refresh logic", "root.sub", nil)
	f.legacy = mustCreate("Legacy auth removed", "root.sub", nil)
	f.orphan = mustCreate("Unrelated note", model.RootScope, nil)

	mustLink := func(from, to model.Entry, edgeType model.EdgeType, weight *float64) model.Edge {
		t.Helper()
		e := model.NewEdge(from.ID, to.ID, edgeType)
		if weight != nil {
			e = e.WithWeight(*weight)
		}
		if err := edges.Create(ctx, &e); err != nil {
			t.Fatalf("create edge %s -[%s]-> %s: %v", from.Content, edgeType, to.Content, err)
		}
		return e
	}

	f.dependsOn = mustLink(f.schema, f.ulids, model.EdgeDependsOn, nil)
	f.contradicts = mustLink(f.schema, f.legacy, model.EdgeContradicts, nil)
	f.supersedes = mustLink(f.oauth, f.refresh, model.EdgeSupersedes, nil)
	explicitWeight := 0.9
	f.elaborates = mustLink(f.oauth, f.schema, model.EdgeElaborates, &explicitWeight)
	f.relatedTo = mustLink(f.refresh, f.ulids, model.EdgeRelatedTo, nil)

	// Dangling edge: an edge whose peer entry was never created. The schema
	// enforces FOREIGN KEY edges(to_id) REFERENCES entries(id) ON DELETE
	// CASCADE (storage/sqlite/migrations/001_initial.sql), so a genuinely
	// dangling row cannot be inserted or survive entry deletion through the
	// storage layer. decoratedEdgeRepo simulates the same observable shape
	// (an edge whose peer Get() 404s) by injecting one synthetic edge into
	// EdgesFrom(schema.ID) without touching the database.
	f.dangling = model.NewEdge(f.schema.ID, model.NewID(), model.EdgeRelatedTo)
	decoratedEdges := &decoratedEdgeRepo{EdgeRepo: edges, danglingFrom: f.schema.ID, dangling: f.dangling}

	engine := query.New(entries, decoratedEdges, nil)
	f.server = New(entries, decoratedEdges, scopesRepo, db.Labels(), engine, model.RootScope)
	return f
}

// decoratedEdgeRepo wraps a real storage.EdgeRepo, injecting one synthetic
// edge into EdgesFrom(danglingFrom, ...) results. See newTestFixture for why
// this simulates a dangling edge instead of persisting one for real.
type decoratedEdgeRepo struct {
	storage.EdgeRepo
	danglingFrom model.ID
	dangling     model.Edge
}

func (d *decoratedEdgeRepo) EdgesFrom(ctx context.Context, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	edges, err := d.EdgeRepo.EdgesFrom(ctx, entryID, filter)
	if err != nil {
		return nil, err
	}
	if entryID == d.danglingFrom && (filter.Type == "" || filter.Type == d.dangling.Type) {
		edges = append(edges, d.dangling)
	}
	return edges, nil
}

// get issues a GET request against the fixture's server and decodes the JSON
// response body into dst (if non-nil). Returns the response status.
func (f *testFixture) get(t *testing.T, path string, dst any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); dst != nil || rec.Code >= 400 {
		if ct != "application/json" {
			t.Errorf("GET %s: Content-Type = %q, want application/json", path, ct)
		}
	}

	if dst != nil {
		if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
			t.Fatalf("GET %s: decode response: %v (body: %s)", path, err, rec.Body.String())
		}
	}
	return rec.Code
}

// assertUniqueGraphIDs fails the test if got.Nodes or got.Edges contains any
// ID more than once. This defends the round-2 dedup invariant directly: a
// map[string]bool built from a slice silently collapses duplicates, so
// membership-only checks (as used throughout this file to assert WHICH
// nodes/edges are present) cannot catch a regression that emits the same
// node/edge twice on the wire -- which crashes the frontend's graph canvas
// (duplicate cytoscape element IDs).
func assertUniqueGraphIDs(t *testing.T, got Graph) {
	t.Helper()
	nodeCount := make(map[string]int, len(got.Nodes))
	for _, n := range got.Nodes {
		nodeCount[n.ID]++
	}
	for id, count := range nodeCount {
		if count > 1 {
			t.Errorf("node %s appears %d times in nodes, want exactly once", id, count)
		}
	}
	edgeCount := make(map[string]int, len(got.Edges))
	for _, e := range got.Edges {
		edgeCount[e.ID]++
	}
	for id, count := range edgeCount {
		if count > 1 {
			t.Errorf("edge %s appears %d times in edges, want exactly once", id, count)
		}
	}
}

// ---------------------------------------------------------------------------
// /api/meta
// ---------------------------------------------------------------------------

func TestHandleMeta(t *testing.T) {
	f := newTestFixture(t)

	var got metaResponse
	if code := f.get(t, "/api/meta", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if got.DefaultScope != model.RootScope {
		t.Errorf("default_scope = %q, want %q", got.DefaultScope, model.RootScope)
	}
	wantScopes := map[string]bool{"root": true, "root.sub": true}
	for _, s := range got.Scopes {
		delete(wantScopes, s)
	}
	if len(wantScopes) != 0 {
		t.Errorf("scopes = %v, missing %v", got.Scopes, wantScopes)
	}

	wantLabels := map[string]bool{"convention": true, "security": true}
	for _, l := range got.Labels {
		delete(wantLabels, l)
	}
	if len(wantLabels) != 0 {
		t.Errorf("labels = %v, missing %v", got.Labels, wantLabels)
	}

	wantEdgeTypes := []string{"depends-on", "contradicts", "supersedes", "elaborates", "related-to"}
	if len(got.EdgeTypes) != len(wantEdgeTypes) {
		t.Fatalf("edge_types = %v, want %v", got.EdgeTypes, wantEdgeTypes)
	}
	for i, et := range wantEdgeTypes {
		if got.EdgeTypes[i] != et {
			t.Errorf("edge_types[%d] = %q, want %q", i, got.EdgeTypes[i], et)
		}
	}
}

// ---------------------------------------------------------------------------
// /api/graph
// ---------------------------------------------------------------------------

func TestHandleGraph_ScopeFilter(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	if code := f.get(t, "/api/graph?scope=root.sub", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)

	if got.Nodes == nil || got.Edges == nil {
		t.Fatalf("nodes/edges must never be null: %+v", got)
	}

	primary := map[string]bool{}
	external := map[string]bool{}
	for _, n := range got.Nodes {
		if n.External {
			external[n.ID] = true
		} else {
			primary[n.ID] = true
		}
	}

	wantPrimary := map[string]bool{
		f.oauth.ID.String():   true,
		f.refresh.ID.String(): true,
		f.legacy.ID.String():  true,
	}
	if len(primary) != len(wantPrimary) {
		t.Fatalf("primary nodes = %v, want exactly %v", primary, wantPrimary)
	}
	for id := range wantPrimary {
		if !primary[id] {
			t.Errorf("missing expected primary node %s in scope filter result", id)
		}
	}

	// ROUND 2: schema (root scope) is pulled in as external via the
	// elaborates (oauth->schema) and contradicts (schema->legacy) boundary
	// edges; ulids (root scope) via the related-to (refresh->ulids) boundary
	// edge. orphan has no edges at all, so it must not appear at all.
	wantExternal := map[string]bool{
		f.schema.ID.String(): true,
		f.ulids.ID.String():  true,
	}
	if len(external) != len(wantExternal) {
		t.Fatalf("external nodes = %v, want exactly %v", external, wantExternal)
	}
	for id := range wantExternal {
		if !external[id] {
			t.Errorf("missing expected external node %s in scope filter result", id)
		}
	}
	if primary[f.orphan.ID.String()] || external[f.orphan.ID.String()] {
		t.Errorf("orphan (no edges) must not appear in scope filter result")
	}

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	// depends-on (schema->ulids) touches NEITHER primary node, so it must
	// stay excluded even under the round-2 boundary-edge rule.
	if edgeIDs[f.dependsOn.ID.String()] {
		t.Errorf("depends-on edge %s touches no primary node and must be excluded", f.dependsOn.ID)
	}
	for _, want := range []model.Edge{f.supersedes, f.elaborates, f.contradicts, f.relatedTo} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("expected boundary/interior edge %s in scope filter result", want.ID)
		}
	}
}

func TestHandleGraph_BothEndpointsRule(t *testing.T) {
	f := newTestFixture(t)

	// Whole-graph snapshot: every entry is primary, so no external nodes are
	// possible and the round-1 both-endpoints-present rule still governs.
	var got Graph
	if code := f.get(t, "/api/graph", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	// dangling points at a non-existent entry: there is no entry data to
	// build a Node from, so it must be excluded even though its source
	// (schema) is present.
	if edgeIDs[f.dangling.ID.String()] {
		t.Errorf("dangling edge %s must be excluded (peer entry does not exist)", f.dangling.ID)
	}
	for _, want := range []model.Edge{f.dependsOn, f.contradicts, f.supersedes, f.elaborates, f.relatedTo} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("expected edge %s (both endpoints present) in whole-graph result", want.ID)
		}
	}
}

func TestHandleGraph_NoFilterNoTruncation_ZeroExternals(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	if code := f.get(t, "/api/graph", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)
	if got.Truncated {
		t.Fatalf("truncated = true, want false (default limit 500 comfortably covers the fixture)")
	}
	for _, n := range got.Nodes {
		if n.External {
			t.Errorf("node %s: external = true, want false (no scope/label filter, no truncation)", n.ID)
		}
	}
}

func TestHandleGraph_LabelBoundary(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	if code := f.get(t, "/api/graph?label=security", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)

	primary := map[string]bool{}
	external := map[string]bool{}
	for _, n := range got.Nodes {
		if n.External {
			external[n.ID] = true
		} else {
			primary[n.ID] = true
		}
	}

	// Only oauth carries the "security" label.
	if want := map[string]bool{f.oauth.ID.String(): true}; len(primary) != len(want) || !primary[f.oauth.ID.String()] {
		t.Fatalf("primary nodes = %v, want exactly %v", primary, want)
	}

	// oauth's out-edges reach refresh (supersedes) and schema (elaborates);
	// neither carries the "security" label, so both are external.
	wantExternal := map[string]bool{
		f.refresh.ID.String(): true,
		f.schema.ID.String():  true,
	}
	if len(external) != len(wantExternal) {
		t.Fatalf("external nodes = %v, want exactly %v", external, wantExternal)
	}
	for id := range wantExternal {
		if !external[id] {
			t.Errorf("missing expected external node %s in label filter result", id)
		}
	}

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	for _, want := range []model.Edge{f.supersedes, f.elaborates} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("expected boundary edge %s in label filter result", want.ID)
		}
	}
	for _, notWant := range []model.Edge{f.dependsOn, f.contradicts, f.relatedTo} {
		if edgeIDs[notWant.ID.String()] {
			t.Errorf("edge %s touches no primary node and must be excluded", notWant.ID)
		}
	}
}

// TestHandleGraph_TruncationBoundary uses its own tiny fixture with
// explicit, well-separated CreatedAt values (rather than the shared
// testFixture, whose entries are all created via back-to-back time.Now()
// calls and are not safe to order deterministically) so that limit=2's
// clipped-out entry is a known, asserted identity: EntryRepo.List orders by
// created_at DESC, so "oldest" is the one truncation clips.
func TestHandleGraph_TruncationBoundary(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	entries := db.Entries()
	edgeRepo := db.Edges()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreateAt := func(content string, offset time.Duration) model.Entry {
		t.Helper()
		e := model.NewEntry(content, model.Source{Type: model.SourceManual, Reference: "test"})
		e.Scope = model.RootScope
		e.CreatedAt = base.Add(offset)
		e.Freshness.ObservedAt = e.CreatedAt
		result, err := entries.CreateOrUpdate(ctx, &e)
		if err != nil {
			t.Fatalf("create entry %q: %v", content, err)
		}
		return *result
	}

	oldest := mustCreateAt("Oldest entry", 0)
	middle := mustCreateAt("Middle entry", time.Second)
	newest := mustCreateAt("Newest entry", 2*time.Second)

	edge := model.NewEdge(newest.ID, oldest.ID, model.EdgeRelatedTo)
	if err := edgeRepo.Create(ctx, &edge); err != nil {
		t.Fatalf("create edge: %v", err)
	}

	engine := query.New(entries, edgeRepo, nil)
	server := New(entries, edgeRepo, db.Scopes(), db.Labels(), engine, model.RootScope)

	req := httptest.NewRequest(http.MethodGet, "/api/graph?limit=2", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got Graph
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertUniqueGraphIDs(t, got)

	if !got.Truncated {
		t.Fatalf("truncated = false, want true (limit=2 of 3 entries)")
	}

	byID := map[string]Node{}
	for _, n := range got.Nodes {
		byID[n.ID] = n
	}

	newestNode, ok := byID[newest.ID.String()]
	if !ok || newestNode.External {
		t.Errorf("newest node = %+v (ok=%v), want present and primary (external=false)", newestNode, ok)
	}
	middleNode, ok := byID[middle.ID.String()]
	if !ok || middleNode.External {
		t.Errorf("middle node = %+v (ok=%v), want present and primary (external=false)", middleNode, ok)
	}
	oldestNode, ok := byID[oldest.ID.String()]
	if !ok || !oldestNode.External {
		t.Errorf("oldest node = %+v (ok=%v), want present and external=true (truncation-clipped boundary peer)", oldestNode, ok)
	}

	foundEdge := false
	for _, e := range got.Edges {
		if e.ID == edge.ID.String() {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Errorf("expected boundary edge %s (newest->oldest) in truncated result", edge.ID)
	}
}

// TestHandleGraph_ExternalNodeShape asserts the external node carries the
// full Node projection (not a stripped-down peer ref) plus external:true,
// and that a primary node's raw JSON omits the "external" key entirely
// (not just false) per the contract's "OMITTED for primary nodes" wording.
func TestHandleGraph_ExternalNodeShape(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/graph?scope=root.sub", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()

	var got Graph
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertUniqueGraphIDs(t, got)
	var schemaNode, oauthNode Node
	for _, n := range got.Nodes {
		switch n.ID {
		case f.schema.ID.String():
			schemaNode = n
		case f.oauth.ID.String():
			oauthNode = n
		}
	}
	if schemaNode.ID == "" {
		t.Fatalf("schema external node not found in %+v", got.Nodes)
	}
	if !schemaNode.External {
		t.Errorf("schema node external = false, want true")
	}
	if schemaNode.Title != f.schema.Title || schemaNode.Content != f.schema.Content ||
		schemaNode.Scope != f.schema.Scope || schemaNode.SourceType != string(f.schema.Source.Type) {
		t.Errorf("external node = %+v, want full projection of %+v", schemaNode, f.schema)
	}

	// Raw JSON check: a primary node (oauth) must omit "external" entirely,
	// not merely encode it as false.
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	found := false
	for _, n := range raw.Nodes {
		if n["id"] != f.oauth.ID.String() {
			continue
		}
		found = true
		if _, present := n["external"]; present {
			t.Errorf("primary node raw JSON = %v, want no \"external\" key", n)
		}
	}
	if !found {
		t.Fatalf("oauth primary node not found in raw JSON: %s", body)
	}
	if oauthNode.External {
		t.Errorf("oauth node external = true, want false")
	}
}

func TestHandleGraph_Truncated(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	if code := f.get(t, "/api/graph?limit=2", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)
	primaryCount := 0
	for _, n := range got.Nodes {
		if !n.External {
			primaryCount++
		}
	}
	if primaryCount != 2 {
		t.Fatalf("primary nodes = %d, want 2 (limit)", primaryCount)
	}
	if !got.Truncated {
		t.Errorf("truncated = false, want true when limit clips the node set")
	}

	var full Graph
	if code := f.get(t, "/api/graph?limit=100", &full); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if full.Truncated {
		t.Errorf("truncated = true, want false when limit does not clip the node set")
	}
}

func TestHandleGraph_BadLimit(t *testing.T) {
	f := newTestFixture(t)
	if code := f.get(t, "/api/graph?limit=notanumber", nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------------
// /api/entry/{id}
// ---------------------------------------------------------------------------

func TestHandleEntry_HappyPath(t *testing.T) {
	f := newTestFixture(t)

	var got entryDetail
	code := f.get(t, "/api/entry/"+f.schema.ID.String(), &got)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if got.Entry.ID != f.schema.ID {
		t.Errorf("entry.id = %s, want %s", got.Entry.ID, f.schema.ID)
	}
	if got.Entry.Embedding != nil || got.Entry.EmbeddingDim != 0 || got.Entry.EmbeddingModel != "" {
		t.Errorf("entry embedding fields must be stripped, got %+v", got.Entry)
	}
	if got.Node.ID != f.schema.ID.String() {
		t.Errorf("node.id = %s, want %s", got.Node.ID, f.schema.ID)
	}

	// edges_out: depends-on -> ulids, contradicts -> legacy, related-to -> dangling.
	if len(got.EdgesOut) != 3 {
		t.Fatalf("edges_out = %d, want 3", len(got.EdgesOut))
	}
	// edges_in: elaborates <- oauth (explicit weight 0.9).
	if len(got.EdgesIn) != 1 {
		t.Fatalf("edges_in = %d, want 1", len(got.EdgesIn))
	}
	in := got.EdgesIn[0]
	if in.Edge.ID != f.elaborates.ID.String() {
		t.Errorf("edges_in[0].edge.id = %s, want %s", in.Edge.ID, f.elaborates.ID)
	}
	if !in.Edge.Explicit || in.Edge.Weight != 0.9 {
		t.Errorf("edges_in[0].edge = %+v, want explicit weight 0.9", in.Edge)
	}
	if in.Peer.ID != f.oauth.ID.String() || in.Peer.Title != f.oauth.Title || in.Peer.Content != f.oauth.Content {
		t.Errorf("edges_in[0].peer = %+v, want oauth entry", in.Peer)
	}

	// conflicts: contradicts edge to legacy.
	if len(got.Conflicts) != 1 || got.Conflicts[0].ID != f.legacy.ID.String() || got.Conflicts[0].Content != f.legacy.Content {
		t.Errorf("conflicts = %+v, want [legacy] with content", got.Conflicts)
	}
}

func TestHandleEntry_DanglingPeerFallback(t *testing.T) {
	f := newTestFixture(t)

	var got entryDetail
	if code := f.get(t, "/api/entry/"+f.schema.ID.String(), &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	found := false
	for _, ep := range got.EdgesOut {
		if ep.Edge.ID == f.dangling.ID.String() {
			found = true
			if ep.Peer.Title != "(missing)" {
				t.Errorf("dangling peer title = %q, want %q", ep.Peer.Title, "(missing)")
			}
			if ep.Peer.Content != "" {
				t.Errorf("dangling peer content = %q, want empty", ep.Peer.Content)
			}
			if ep.Peer.ID != f.dangling.ToID.String() {
				t.Errorf("dangling peer id = %q, want %q", ep.Peer.ID, f.dangling.ToID.String())
			}
		}
	}
	if !found {
		t.Fatalf("dangling edge %s missing from edges_out", f.dangling.ID)
	}
}

func TestHandleEntry_NotFound(t *testing.T) {
	f := newTestFixture(t)
	var got errorResponse
	code := f.get(t, "/api/entry/"+model.NewID().String(), &got)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestHandleEntry_BadULID(t *testing.T) {
	f := newTestFixture(t)
	var got errorResponse
	code := f.get(t, "/api/entry/not-a-ulid", &got)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

// ---------------------------------------------------------------------------
// /api/neighbors/{id}
// ---------------------------------------------------------------------------

func TestHandleNeighbors_DirectionAndDepth(t *testing.T) {
	f := newTestFixture(t)

	var out Graph
	if code := f.get(t, "/api/neighbors/"+f.schema.ID.String()+"?direction=out&depth=1", &out); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, out)
	outIDs := map[string]bool{}
	for _, n := range out.Nodes {
		outIDs[n.ID] = true
	}
	// includes start + ulids (depends-on) + legacy (contradicts); dangling
	// edge's peer entry does not exist so it's skipped by Traverse.
	for _, want := range []model.Entry{f.schema, f.ulids, f.legacy} {
		if !outIDs[want.ID.String()] {
			t.Errorf("direction=out missing expected node %s", want.ID)
		}
	}
	if outIDs[f.oauth.ID.String()] {
		t.Errorf("direction=out must not include incoming-only neighbor oauth")
	}

	var in Graph
	if code := f.get(t, "/api/neighbors/"+f.schema.ID.String()+"?direction=in&depth=1", &in); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, in)
	inIDs := map[string]bool{}
	for _, n := range in.Nodes {
		inIDs[n.ID] = true
	}
	if !inIDs[f.oauth.ID.String()] {
		t.Errorf("direction=in missing expected incoming neighbor oauth")
	}
	if inIDs[f.ulids.ID.String()] {
		t.Errorf("direction=in must not include outgoing-only neighbor ulids")
	}

	// depth=2, both directions: from oauth, schema is depth 1, then
	// schema's out-neighbors (ulids, legacy) are depth 2.
	var deep Graph
	if code := f.get(t, "/api/neighbors/"+f.oauth.ID.String()+"?direction=both&depth=2", &deep); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, deep)
	deepIDs := map[string]bool{}
	for _, n := range deep.Nodes {
		deepIDs[n.ID] = true
	}
	for _, want := range []model.Entry{f.oauth, f.schema, f.refresh, f.ulids, f.legacy} {
		if !deepIDs[want.ID.String()] {
			t.Errorf("depth=2 missing expected node %s", want.ID)
		}
	}
}

func TestHandleNeighbors_TypesFilter(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	code := f.get(t, "/api/neighbors/"+f.schema.ID.String()+"?direction=out&types=depends-on", &got)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	if !ids[f.ulids.ID.String()] {
		t.Errorf("types=depends-on missing expected node ulids")
	}
	if ids[f.legacy.ID.String()] {
		t.Errorf("types=depends-on must exclude contradicts-only neighbor legacy")
	}
}

// diamondFixture is a small dedicated backend shaped like a diamond: hub
// depends-on A, hub depends-on B, and A related-to B (the "chord"). A BFS
// tree rooted at hub can never contain the chord -- Traverse stops
// expanding a node's own edges once that node is reached at the
// traversal's max depth, so A's outgoing edges (including the chord to B)
// are never inspected at depth=1. This is the round-3 mesh-vs-tree
// regression fixture.
type diamondFixture struct {
	server                *Server
	hub, a, b             model.Entry
	hubToA, hubToB, chord model.Edge
}

func newDiamondFixture(t *testing.T) *diamondFixture {
	t.Helper()
	ctx := context.Background()

	db, err := sqlite.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	entries := db.Entries()
	edgeRepo := db.Edges()

	mustCreate := func(content string) model.Entry {
		t.Helper()
		e := model.NewEntry(content, model.Source{Type: model.SourceManual, Reference: "test"})
		e.Scope = model.RootScope
		result, err := entries.CreateOrUpdate(ctx, &e)
		if err != nil {
			t.Fatalf("create entry %q: %v", content, err)
		}
		return *result
	}
	mustLink := func(from, to model.Entry, edgeType model.EdgeType) model.Edge {
		t.Helper()
		e := model.NewEdge(from.ID, to.ID, edgeType)
		if err := edgeRepo.Create(ctx, &e); err != nil {
			t.Fatalf("create edge %s -[%s]-> %s: %v", from.Content, edgeType, to.Content, err)
		}
		return e
	}

	f := &diamondFixture{}
	f.hub = mustCreate("Hub")
	f.a = mustCreate("A")
	f.b = mustCreate("B")
	f.hubToA = mustLink(f.hub, f.a, model.EdgeDependsOn)
	f.hubToB = mustLink(f.hub, f.b, model.EdgeDependsOn)
	f.chord = mustLink(f.a, f.b, model.EdgeRelatedTo)

	engine := query.New(entries, edgeRepo, nil)
	f.server = New(entries, edgeRepo, db.Scopes(), db.Labels(), engine, model.RootScope)
	return f
}

func (f *diamondFixture) get(t *testing.T, path string, dst any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if dst != nil {
		if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
			t.Fatalf("GET %s: decode response: %v (body: %s)", path, err, rec.Body.String())
		}
	}
	return rec.Code
}

// TestHandleNeighbors_MeshIncludesChord is the round-3 acceptance-criterion
// regression test: the old BFS-EdgePath-union implementation structurally
// could not include the A->B chord (Traverse never re-examines a node's own
// edges once it's reached at maxDepth), so this proves the mesh rebuild does.
func TestHandleNeighbors_MeshIncludesChord(t *testing.T) {
	f := newDiamondFixture(t)

	var got Graph
	path := "/api/neighbors/" + f.hub.ID.String() + "?direction=both&depth=1"
	if code := f.get(t, path, &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)

	nodeIDs := map[string]bool{}
	for _, n := range got.Nodes {
		nodeIDs[n.ID] = true
	}
	for _, want := range []model.Entry{f.hub, f.a, f.b} {
		if !nodeIDs[want.ID.String()] {
			t.Errorf("missing expected node %s", want.ID)
		}
	}

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	for _, want := range []model.Edge{f.hubToA, f.hubToB, f.chord} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("mesh missing expected edge %s (type %s)", want.ID, want.Type)
		}
	}
	if len(got.Edges) != 3 {
		t.Errorf("edges = %d, want exactly 3 (hub->A, hub->B, chord A->B)", len(got.Edges))
	}

	// Round 3: /api/neighbors never introduces external nodes.
	for _, n := range got.Nodes {
		if n.External {
			t.Errorf("node %s: external = true, want false (neighbors never introduces externals)", n.ID)
		}
	}
}

// TestHandleNeighbors_TypesFiltersTraversalNotMesh proves `types` only
// shapes which nodes Traverse reaches, not the mesh rebuild: both hub->A and
// hub->B are depends-on (so types=depends-on still reaches all three
// nodes), but the A->B chord is related-to and must still appear.
func TestHandleNeighbors_TypesFiltersTraversalNotMesh(t *testing.T) {
	f := newDiamondFixture(t)

	var got Graph
	path := "/api/neighbors/" + f.hub.ID.String() + "?direction=both&depth=1&types=depends-on"
	if code := f.get(t, path, &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertUniqueGraphIDs(t, got)

	nodeIDs := map[string]bool{}
	for _, n := range got.Nodes {
		nodeIDs[n.ID] = true
	}
	for _, want := range []model.Entry{f.hub, f.a, f.b} {
		if !nodeIDs[want.ID.String()] {
			t.Errorf("types=depends-on missing expected node %s", want.ID)
		}
	}

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	if !edgeIDs[f.chord.ID.String()] {
		t.Errorf("types=depends-on: mesh must still include the related-to chord %s (types filters traversal only)", f.chord.ID)
	}
	if len(got.Edges) != 3 {
		t.Errorf("edges = %d, want exactly 3 even with types=depends-on restricting traversal", len(got.Edges))
	}
}

func TestHandleNeighbors_NotFound(t *testing.T) {
	f := newTestFixture(t)
	code := f.get(t, "/api/neighbors/"+model.NewID().String(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestHandleNeighbors_BadULID(t *testing.T) {
	f := newTestFixture(t)
	if code := f.get(t, "/api/neighbors/not-a-ulid", nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------------
// /api/search
// ---------------------------------------------------------------------------

func TestHandleSearch_Results(t *testing.T) {
	f := newTestFixture(t)

	var got searchResponse
	path := "/api/search?" + url.Values{"q": {"ULIDs"}, "scope": {"root"}}.Encode()
	if code := f.get(t, path, &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Results == nil {
		t.Fatalf("results must never be null")
	}
	found := false
	for _, r := range got.Results {
		if r.Node.ID == f.ulids.ID.String() {
			found = true
			if r.Score <= 0 {
				t.Errorf("score = %v, want > 0", r.Score)
			}
		}
	}
	if !found {
		t.Errorf("expected ulids entry in search results for %+v", got.Results)
	}
}

func TestHandleSearch_EmptyQuery(t *testing.T) {
	f := newTestFixture(t)
	var got errorResponse
	code := f.get(t, "/api/search?q=", &got)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}

	// Whitespace-only q is also empty per contract.
	code = f.get(t, "/api/search?"+url.Values{"q": {"   "}}.Encode(), nil)
	if code != http.StatusBadRequest {
		t.Fatalf("whitespace q: status = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------------
// /api/path
// ---------------------------------------------------------------------------

func TestHandlePath_Found(t *testing.T) {
	f := newTestFixture(t)

	// oauth -[elaborates]-> schema -[depends-on]-> ulids
	var got Graph
	path := "/api/path?" + url.Values{"from": {f.oauth.ID.String()}, "to": {f.ulids.ID.String()}}.Encode()
	if code := f.get(t, path, &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	nodeIDs := map[string]bool{}
	for _, n := range got.Nodes {
		nodeIDs[n.ID] = true
	}
	for _, want := range []model.Entry{f.oauth, f.schema, f.ulids} {
		if !nodeIDs[want.ID.String()] {
			t.Errorf("path missing expected node %s", want.ID)
		}
	}
	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	for _, want := range []model.Edge{f.elaborates, f.dependsOn} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("path missing expected edge %s", want.ID)
		}
	}
}

func TestHandlePath_NoPath(t *testing.T) {
	f := newTestFixture(t)

	// orphan has no edges at all, so no path exists to it.
	var got Graph
	path := "/api/path?" + url.Values{"from": {f.schema.ID.String()}, "to": {f.orphan.ID.String()}}.Encode()
	code := f.get(t, path, &got)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no path is not an error)", code)
	}
	if got.Nodes == nil || len(got.Nodes) != 0 {
		t.Errorf("nodes = %v, want empty (not null) on no path", got.Nodes)
	}
	if got.Edges == nil || len(got.Edges) != 0 {
		t.Errorf("edges = %v, want empty (not null) on no path", got.Edges)
	}
	if got.Truncated {
		t.Errorf("truncated = true, want false")
	}
}

func TestHandlePath_UnknownEndpoint(t *testing.T) {
	f := newTestFixture(t)

	unknown := model.NewID().String()
	path := "/api/path?" + url.Values{"from": {unknown}, "to": {f.ulids.ID.String()}}.Encode()
	if code := f.get(t, path, nil); code != http.StatusNotFound {
		t.Fatalf("unknown from: status = %d, want 404", code)
	}

	path = "/api/path?" + url.Values{"from": {f.ulids.ID.String()}, "to": {unknown}}.Encode()
	if code := f.get(t, path, nil); code != http.StatusNotFound {
		t.Fatalf("unknown to: status = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------------
// API routing edge cases: /api/* must never fall through to the static/SPA
// handler, whether the path is unknown or the method is wrong.
// ---------------------------------------------------------------------------

func TestHandleAPI_UnknownPath(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/doesnotexist", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("GET /api/doesnotexist: status = %d, want a non-2xx error", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /api/doesnotexist: Content-Type = %q, want application/json", ct)
	}
	var got errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestHandleAPI_WrongMethod(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/meta", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("POST /api/meta: status = %d, want a non-2xx error", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("POST /api/meta: Content-Type = %q, want application/json", ct)
	}
	var got errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

// ---------------------------------------------------------------------------
// static serving
// ---------------------------------------------------------------------------

func TestHandleStatic_ServesIndex(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Errorf("GET /: empty body, want index.html contents")
	}
}

func TestHandleStatic_UnknownPathFallsBackToIndex(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/graph/some-node-id", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /graph/some-node-id: status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Errorf("GET /graph/some-node-id: empty body, want index.html fallback contents")
	}
}

// sanity: confirm storage.LabelLister is satisfied so nil-labels code path
// isn't accidentally the only one under test.
var _ storage.LabelLister = (*stubNoopLabelLister)(nil)

type stubNoopLabelLister struct{}

func (stubNoopLabelLister) ListLabels(context.Context) ([]string, error) { return nil, nil }

func TestHandleMeta_NilLabelLister(t *testing.T) {
	f := newTestFixture(t)
	f.server.labels = nil

	var got metaResponse
	if code := f.get(t, "/api/meta", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Labels == nil || len(got.Labels) != 0 {
		t.Errorf("labels = %v, want empty (not null) when LabelLister is nil", got.Labels)
	}
}
