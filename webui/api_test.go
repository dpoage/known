package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

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

	if got.Nodes == nil || got.Edges == nil {
		t.Fatalf("nodes/edges must never be null: %+v", got)
	}

	gotIDs := map[string]bool{}
	for _, n := range got.Nodes {
		gotIDs[n.ID] = true
	}
	wantIDs := map[string]bool{
		f.oauth.ID.String():   true,
		f.refresh.ID.String(): true,
		f.legacy.ID.String():  true,
	}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("nodes = %v, want exactly %v", gotIDs, wantIDs)
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("missing expected node %s in scope filter result", id)
		}
	}

	// supersedes (oauth->refresh) has both endpoints in scope; the schema
	// edges (from root scope) must NOT appear.
	foundSupersedes := false
	for _, e := range got.Edges {
		if e.ID == f.supersedes.ID.String() {
			foundSupersedes = true
		}
		if e.ID == f.dependsOn.ID.String() || e.ID == f.contradicts.ID.String() {
			t.Errorf("edge %s from outside the scope filter leaked into results", e.ID)
		}
	}
	if !foundSupersedes {
		t.Errorf("expected supersedes edge (both endpoints in scope) in results")
	}
}

func TestHandleGraph_BothEndpointsRule(t *testing.T) {
	f := newTestFixture(t)

	// Whole-graph snapshot: every edge whose endpoints are both present.
	var got Graph
	if code := f.get(t, "/api/graph", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	edgeIDs := map[string]bool{}
	for _, e := range got.Edges {
		edgeIDs[e.ID] = true
	}
	// dangling points at a non-existent entry, so it must be excluded even
	// though its source (schema) is present.
	if edgeIDs[f.dangling.ID.String()] {
		t.Errorf("dangling edge %s must be excluded (peer not in node set)", f.dangling.ID)
	}
	for _, want := range []model.Edge{f.dependsOn, f.contradicts, f.supersedes, f.elaborates, f.relatedTo} {
		if !edgeIDs[want.ID.String()] {
			t.Errorf("expected edge %s (both endpoints present) in whole-graph result", want.ID)
		}
	}
}

func TestHandleGraph_Truncated(t *testing.T) {
	f := newTestFixture(t)

	var got Graph
	if code := f.get(t, "/api/graph?limit=2", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (limit)", len(got.Nodes))
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
	if in.Peer.ID != f.oauth.ID.String() || in.Peer.Title != f.oauth.Title {
		t.Errorf("edges_in[0].peer = %+v, want oauth entry", in.Peer)
	}

	// conflicts: contradicts edge to legacy.
	if len(got.Conflicts) != 1 || got.Conflicts[0].ID != f.legacy.ID.String() {
		t.Errorf("conflicts = %+v, want [legacy]", got.Conflicts)
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
