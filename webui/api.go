package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// ---------------------------------------------------------------------------
// Wire types (see local://explore-contract.md for the binding shapes)
// ---------------------------------------------------------------------------

// Node is an entry projected for the graph canvas. Embedding data is never
// serialized.
type Node struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Content    string     `json:"content"`
	Scope      string     `json:"scope"`
	Labels     []string   `json:"labels,omitempty"`
	SourceType string     `json:"source_type"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// Edge is a directed, typed, weighted relationship between two nodes.
type Edge struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"`
	Weight    float64   `json:"weight"`
	Explicit  bool      `json:"explicit"`
	CreatedAt time.Time `json:"created_at"`
}

// Graph is the payload of /api/graph, /api/neighbors, and /api/path. Nodes
// and Edges are never null.
type Graph struct {
	Nodes     []Node `json:"nodes"`
	Edges     []Edge `json:"edges"`
	Truncated bool   `json:"truncated"`
}

// peerRef is a lightweight reference to the other end of an edge, used in
// /api/entry's edges_out/edges_in/conflicts.
type peerRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Scope string `json:"scope"`
}

// edgeWithPeer pairs an edge with a projection of the entry on the other end.
type edgeWithPeer struct {
	Edge Edge    `json:"edge"`
	Peer peerRef `json:"peer"`
}

// entryDetail is the /api/entry/{id} response body.
type entryDetail struct {
	Entry     model.Entry    `json:"entry"`
	Node      Node           `json:"node"`
	EdgesOut  []edgeWithPeer `json:"edges_out"`
	EdgesIn   []edgeWithPeer `json:"edges_in"`
	Conflicts []peerRef      `json:"conflicts"`
}

// metaResponse is the /api/meta response body.
type metaResponse struct {
	DefaultScope string   `json:"default_scope"`
	Scopes       []string `json:"scopes"`
	Labels       []string `json:"labels"`
	EdgeTypes    []string `json:"edge_types"`
}

// searchResult pairs a node with its relevance score.
type searchResult struct {
	Node  Node    `json:"node"`
	Score float64 `json:"score"`
}

// searchResponse is the /api/search response body.
type searchResponse struct {
	Results []searchResult `json:"results"`
}

// errorResponse is the body of every non-2xx /api response.
type errorResponse struct {
	Error string `json:"error"`
}

// ---------------------------------------------------------------------------
// Projections
// ---------------------------------------------------------------------------

// newNode projects a model.Entry into the wire Node shape.
func newNode(e model.Entry) Node {
	n := Node{
		ID:         e.ID.String(),
		Title:      e.Title,
		Content:    e.Content,
		Scope:      e.Scope,
		Labels:     e.Labels,
		SourceType: string(e.Source.Type),
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}
	if !e.Freshness.ObservedAt.IsZero() {
		observed := e.Freshness.ObservedAt
		n.ObservedAt = &observed
	}
	return n
}

// newEdge projects a model.Edge into the wire Edge shape. Weight is always
// the effective (defaulted) weight; Explicit reports whether the stored
// weight was ever set or reinforced.
func newEdge(e model.Edge) Edge {
	return Edge{
		ID:        e.ID.String(),
		From:      e.FromID.String(),
		To:        e.ToID.String(),
		Type:      string(e.Type),
		Weight:    e.EffectiveWeight(),
		Explicit:  e.Weight != nil,
		CreatedAt: e.CreatedAt,
	}
}

// stripEmbedding returns a copy of e with embedding fields zeroed. Mirrors
// cmd/list.go's stripEmbeddings; webui cannot import cmd (import cycle), so
// it carries its own equivalent.
func stripEmbedding(e model.Entry) model.Entry {
	e.Embedding = nil
	e.EmbeddingDim = 0
	e.EmbeddingModel = ""
	return e
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// parseIntParam parses raw as a positive integer, defaulting to def when raw
// is empty and clamping to max. Non-numeric or non-positive input is an error
// (400 malformed input per contract).
func parseIntParam(raw string, def, max int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%q must be a positive integer", raw)
	}
	if n > max {
		n = max
	}
	return n, nil
}

// parseDirection parses the neighbors direction query param. Contract values
// are exactly out|in|both (unlike cmd/related.go's out|outgoing|in|incoming|both).
func parseDirection(raw string) (query.GraphDirection, error) {
	switch strings.ToLower(raw) {
	case "", "both":
		return query.Both, nil
	case "out":
		return query.Outgoing, nil
	case "in":
		return query.Incoming, nil
	default:
		return 0, fmt.Errorf("invalid direction %q: must be out, in, or both", raw)
	}
}

// parseEdgeTypes splits a CSV of edge type names, trimming whitespace and
// dropping empty segments.
func parseEdgeTypes(raw string) []model.EdgeType {
	if raw == "" {
		return nil
	}
	var types []model.EdgeType
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			types = append(types, model.EdgeType(part))
		}
	}
	return types
}

// sortEdges orders edges by ID for deterministic responses; edges are
// collected from maps (dedup by ID) so iteration order is otherwise random.
func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleMeta implements GET /api/meta.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	scopeList, err := s.scopes.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scopes := make([]string, 0, len(scopeList))
	for _, sc := range scopeList {
		scopes = append(scopes, sc.Path)
	}

	labels := []string{}
	if s.labels != nil {
		ls, err := s.labels.ListLabels(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if ls != nil {
			labels = ls
		}
	}

	predefined := model.PredefinedEdgeTypes()
	edgeTypes := make([]string, 0, len(predefined))
	for _, et := range predefined {
		edgeTypes = append(edgeTypes, string(et))
	}

	writeJSON(w, http.StatusOK, metaResponse{
		DefaultScope: s.defaultScope,
		Scopes:       scopes,
		Labels:       labels,
		EdgeTypes:    edgeTypes,
	})
}

// handleGraph implements GET /api/graph?scope=&label=&limit=.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, err := parseIntParam(q.Get("limit"), 500, 2000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	filter := storage.EntryFilter{ScopePrefix: q.Get("scope"), Limit: limit}
	if label := q.Get("label"); label != "" {
		filter.Labels = []string{label}
	}

	ctx := r.Context()
	entries, err := s.entries.List(ctx, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	graph, err := s.buildGraph(ctx, entries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	graph.Truncated = limit > 0 && len(entries) >= limit

	writeJSON(w, http.StatusOK, graph)
}

// buildGraph projects entries to nodes and includes every edge whose both
// endpoints are present in the resulting node set.
func (s *Server) buildGraph(ctx context.Context, entries []model.Entry) (Graph, error) {
	nodeSet := make(map[string]bool, len(entries))
	nodes := make([]Node, 0, len(entries))
	for _, e := range entries {
		nodeSet[e.ID.String()] = true
		nodes = append(nodes, newNode(e))
	}

	edges := make([]Edge, 0)
	for _, e := range entries {
		out, err := s.edges.EdgesFrom(ctx, e.ID, storage.EdgeFilter{})
		if err != nil {
			return Graph{}, fmt.Errorf("edges from %s: %w", e.ID, err)
		}
		for _, edge := range out {
			if nodeSet[edge.ToID.String()] {
				edges = append(edges, newEdge(edge))
			}
		}
	}

	return Graph{Nodes: nodes, Edges: edges}, nil
}

// handleEntry implements GET /api/entry/{id}.
func (s *Server) handleEntry(w http.ResponseWriter, r *http.Request) {
	id, err := model.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id: "+err.Error())
		return
	}

	ctx := r.Context()
	entry, err := s.entries.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out, err := s.edges.EdgesFrom(ctx, id, storage.EdgeFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := s.edges.EdgesTo(ctx, id, storage.EdgeFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	conflicts, err := s.edges.FindConflicts(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	detail := entryDetail{
		Entry:     stripEmbedding(*entry),
		Node:      newNode(*entry),
		EdgesOut:  make([]edgeWithPeer, 0, len(out)),
		EdgesIn:   make([]edgeWithPeer, 0, len(in)),
		Conflicts: make([]peerRef, 0, len(conflicts)),
	}

	for _, edge := range out {
		peer, err := s.peerRef(ctx, edge.ToID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		detail.EdgesOut = append(detail.EdgesOut, edgeWithPeer{Edge: newEdge(edge), Peer: peer})
	}
	for _, edge := range in {
		peer, err := s.peerRef(ctx, edge.FromID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		detail.EdgesIn = append(detail.EdgesIn, edgeWithPeer{Edge: newEdge(edge), Peer: peer})
	}
	for _, c := range conflicts {
		detail.Conflicts = append(detail.Conflicts, peerRef{ID: c.ID.String(), Title: c.Title, Scope: c.Scope})
	}

	writeJSON(w, http.StatusOK, detail)
}

// peerRef resolves the entry on the other end of an edge into a peerRef.
// A dangling edge (peer entry deleted) falls back to a "(missing)" title
// rather than failing the whole /api/entry response.
func (s *Server) peerRef(ctx context.Context, id model.ID) (peerRef, error) {
	peer, err := s.entries.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return peerRef{ID: id.String(), Title: "(missing)"}, nil
	}
	if err != nil {
		return peerRef{}, err
	}
	return peerRef{ID: peer.ID.String(), Title: peer.Title, Scope: peer.Scope}, nil
}

// handleNeighbors implements GET /api/neighbors/{id}?direction=&depth=&types=.
func (s *Server) handleNeighbors(w http.ResponseWriter, r *http.Request) {
	id, err := model.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id: "+err.Error())
		return
	}

	q := r.URL.Query()
	dir, err := parseDirection(q.Get("direction"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	depth, err := parseIntParam(q.Get("depth"), 1, 5)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	results, err := s.engine.Traverse(r.Context(), query.GraphOptions{
		StartID:      id,
		Direction:    dir,
		MaxDepth:     depth,
		EdgeTypes:    parseEdgeTypes(q.Get("types")),
		IncludeStart: true,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, graphFromResults(results))
}

// graphFromResults builds a Graph from traversal results: nodes are the
// start entry plus every reached entry; edges are the union of every
// result's EdgePath, deduped by edge ID, restricted to edges whose both
// endpoints are in the node set.
func graphFromResults(results []query.Result) Graph {
	nodeSet := make(map[string]bool, len(results))
	nodes := make([]Node, 0, len(results))
	for _, res := range results {
		nodeSet[res.Entry.ID.String()] = true
		nodes = append(nodes, newNode(res.Entry))
	}

	edgeSet := make(map[string]Edge)
	for _, res := range results {
		for _, e := range res.EdgePath {
			if nodeSet[e.FromID.String()] && nodeSet[e.ToID.String()] {
				edgeSet[e.ID.String()] = newEdge(e)
			}
		}
	}
	edges := make([]Edge, 0, len(edgeSet))
	for _, e := range edgeSet {
		edges = append(edges, e)
	}
	sortEdges(edges)

	return Graph{Nodes: nodes, Edges: edges}
}

// handleSearch implements GET /api/search?q=&scope=&limit=.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	text := strings.TrimSpace(q.Get("q"))
	if text == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}

	scope := q.Get("scope")
	if scope == "" {
		scope = s.defaultScope
	}

	limit, err := parseIntParam(q.Get("limit"), 20, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	results, err := s.engine.SearchText(r.Context(), query.VectorOptions{
		Text:  text,
		Scope: scope,
		Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]searchResult, 0, len(results))
	for _, res := range results {
		out = append(out, searchResult{Node: newNode(res.Entry), Score: res.Score})
	}

	writeJSON(w, http.StatusOK, searchResponse{Results: out})
}

// handlePath implements GET /api/path?from=&to=&max_depth=.
func (s *Server) handlePath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	fromID, err := model.ParseID(q.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from id: "+err.Error())
		return
	}
	toID, err := model.ParseID(q.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to id: "+err.Error())
		return
	}
	maxDepth, err := parseIntParam(q.Get("max_depth"), 6, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()

	fromEntry, err := s.entries.Get(ctx, fromID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "from entry not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.entries.Get(ctx, toID); errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "to entry not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	results, err := s.engine.FindPath(ctx, fromID, toID, maxDepth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		writeJSON(w, http.StatusOK, Graph{Nodes: []Node{}, Edges: []Edge{}})
		return
	}

	writeJSON(w, http.StatusOK, pathGraph(*fromEntry, results))
}

// pathGraph builds a Graph containing the start entry, every hop entry, and
// the deduped union of all hop edges.
func pathGraph(from model.Entry, results []query.Result) Graph {
	nodes := make([]Node, 0, len(results)+1)
	nodes = append(nodes, newNode(from))

	edgeSet := make(map[string]Edge)
	for _, res := range results {
		nodes = append(nodes, newNode(res.Entry))
		for _, e := range res.EdgePath {
			edgeSet[e.ID.String()] = newEdge(e)
		}
	}
	edges := make([]Edge, 0, len(edgeSet))
	for _, e := range edgeSet {
		edges = append(edges, e)
	}
	sortEdges(edges)

	return Graph{Nodes: nodes, Edges: edges}
}
