// Package query provides a query engine for the knowledge graph that combines
// vector similarity search, graph traversal, hybrid queries, and conflict detection.
//
// The engine operates on the storage interfaces (storage.EntryRepo, storage.EdgeRepo)
// and uses the embed.Embedder interface to embed query strings for vector search.
package query

import (
	"context"
	"math"
	"time"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// ReachMethod describes how a result was discovered.
type ReachMethod string

const (
	// ReachDirect means the result was found directly via vector search.
	ReachDirect ReachMethod = "direct"
	// ReachTraversal means the result was found via graph traversal.
	ReachTraversal ReachMethod = "traversal"
	// ReachExpansion means the result was found via hybrid graph expansion
	// after an initial vector search.
	ReachExpansion ReachMethod = "expansion"
)

// Result is a single query result with full metadata, provenance, scores,
// conflict flags, and information about how the result was reached.
type Result struct {
	Entry      model.Entry  `json:"entry"`
	Score      float64      `json:"score"`                 // similarity score (higher is better, 0-1)
	Distance   float64      `json:"distance"`              // raw distance from vector search (lower is closer)
	Reach      ReachMethod  `json:"reach"`                 // how this result was discovered
	Depth      int          `json:"depth"`                 // graph traversal depth (0 for direct search)
	EdgePath   []model.Edge `json:"edge_path,omitempty"`   // edges traversed to reach this result
	HasConflict bool        `json:"has_conflict"`          // whether conflicts exist for this entry
	Conflicts  []model.ID   `json:"conflicts,omitempty"`   // IDs of conflicting entries
}

// ConflictPair represents a pair of entries connected by a contradicts edge.
type ConflictPair struct {
	EntryA model.Entry `json:"entry_a"`
	EntryB model.Entry `json:"entry_b"`
	Edge   model.Edge  `json:"edge"`
}

// VectorOptions configures a vector similarity search.
type VectorOptions struct {
	// Text is the query string to embed and search for.
	Text string

	// Scope limits results to entries within this scope (and descendants).
	Scope string

	// Limit is the maximum number of results to return.
	Limit int

	// Threshold is the minimum similarity score (0-1) a result must have
	// to be included. Results below this score are filtered out.
	// Zero means no threshold filtering.
	Threshold float64

	// RecencyWeight controls how much recency affects the final score.
	// 0.0 means pure similarity, 1.0 means pure recency.
	// The blended score is: (1 - RecencyWeight) * similarity + RecencyWeight * freshness.
	RecencyWeight float64

	// RecencyHalfLife is the duration after which an entry's freshness
	// decays to 0.5. Defaults to 7 days (168h) if zero.
	RecencyHalfLife time.Duration

	// ExcludeContent filters out results whose content contains any of these substrings.
	ExcludeContent []string

	// Metric is the similarity metric to use. Defaults to Cosine.
	Metric storage.SimilarityMetric
}

// GraphDirection specifies the direction of graph traversal.
type GraphDirection int

const (
	// Outgoing follows edges from the source entry.
	Outgoing GraphDirection = iota
	// Incoming follows edges to the source entry.
	Incoming
	// Both follows edges in both directions.
	Both
)

// GraphOptions configures a graph traversal query.
type GraphOptions struct {
	// StartID is the entry to start traversal from.
	StartID model.ID

	// Direction controls which edges to follow.
	Direction GraphDirection

	// MaxDepth is the maximum number of hops. Must be >= 1.
	MaxDepth int

	// EdgeTypes filters traversal to only follow these edge types.
	// An empty slice means follow all edge types.
	EdgeTypes []model.EdgeType

	// IncludeStart controls whether the starting entry is included in results.
	IncludeStart bool
}

// HybridOptions configures a hybrid vector + graph expansion query.
type HybridOptions struct {
	// Vector is the vector search configuration for the initial search.
	Vector VectorOptions

	// ExpandDepth is the number of graph hops to expand from each
	// vector search result. Must be >= 1.
	ExpandDepth int

	// ExpandDirection controls which edges to follow during expansion.
	ExpandDirection GraphDirection

	// ExpandEdgeTypes filters expansion to only follow these edge types.
	ExpandEdgeTypes []model.EdgeType
}

// Engine provides query operations over the knowledge graph.
type Engine struct {
	entries  storage.EntryRepo
	edges    storage.EdgeRepo
	embedder embed.Embedder
}

// New creates a new query engine backed by the given repositories and embedder.
func New(entries storage.EntryRepo, edges storage.EdgeRepo, embedder embed.Embedder) *Engine {
	return &Engine{
		entries:  entries,
		edges:    edges,
		embedder: embedder,
	}
}

// distanceToScore converts a distance value to a 0-1 similarity score,
// accounting for the metric used.
//
// For cosine distance (range 0-2), score = 1 - distance/2.
// For L2 distance (range 0-∞), score = 1 / (1 + distance).
// For inner product (pgvector returns negative inner product), score = 1 / (1 + distance).
func distanceToScore(distance float64, metric storage.SimilarityMetric) float64 {
	var score float64
	switch metric {
	case storage.Cosine:
		// Cosine distance in pgvector ranges from 0 to 2.
		score = 1.0 - distance/2.0
	case storage.L2:
		// L2 distance ranges from 0 to ∞. Use inverse mapping.
		score = 1.0 / (1.0 + distance)
	case storage.InnerProduct:
		// pgvector returns negative inner product, so distance ≥ 0 for normalized vectors.
		score = 1.0 / (1.0 + distance)
	default:
		score = 1.0 - distance/2.0
	}
	return clamp(score, 0, 1)
}

// freshnessScore computes a time-decay freshness value between 0 and 1.
// It uses exponential decay: freshness = exp(-ln(2) * age / halfLife).
func freshnessScore(createdAt time.Time, halfLife time.Duration) float64 {
	if halfLife <= 0 {
		halfLife = 7 * 24 * time.Hour // default 7 days
	}
	age := time.Since(createdAt)
	if age < 0 {
		return 1.0
	}
	return math.Exp(-math.Ln2 * float64(age) / float64(halfLife))
}

// blendScore combines a similarity score with a freshness score using the
// given recency weight: (1 - weight) * similarity + weight * freshness.
func blendScore(similarity, freshness, recencyWeight float64) float64 {
	w := clamp(recencyWeight, 0, 1)
	return (1-w)*similarity + w*freshness
}

// edgeWeight returns the effective weight of an edge.
// If the weight is nil (unset), it defaults to 1.0 (full strength).
func edgeWeight(edge model.Edge) float64 {
	if edge.Weight == nil {
		return 1.0
	}
	return *edge.Weight
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// enrichConflicts checks each result for contradicts edges and populates
// the HasConflict and Conflicts fields.
func (e *Engine) enrichConflicts(ctx context.Context, results []Result) error {
	for i := range results {
		conflicts, err := e.edges.FindConflicts(ctx, results[i].Entry.ID)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			results[i].HasConflict = true
			ids := make([]model.ID, len(conflicts))
			for j, c := range conflicts {
				ids[j] = c.ID
			}
			results[i].Conflicts = ids
		}
	}
	return nil
}
