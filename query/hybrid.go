package query

import (
	"context"
	"errors"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// SearchHybrid performs a vector similarity search followed by graph expansion.
//
// When TextSearch is enabled, it also runs an FTS5 text search and fuses the
// two result lists using Reciprocal Rank Fusion (RRF). RRF ranks by position
// rather than raw score, so incompatible scales (cosine vs BM25) don't matter.
//
// After fusion (or pure vector search), each result is expanded outward along
// graph edges up to ExpandDepth hops. The final result set is deduplicated by
// entry ID.
func (e *Engine) SearchHybrid(ctx context.Context, opts HybridOptions) ([]Result, error) {
	if opts.ExpandDepth <= 0 {
		opts.ExpandDepth = 1
	}

	// Phase 1: Vector search.
	vectorResults, err := e.SearchVector(ctx, opts.Vector)
	if err != nil {
		return nil, fmt.Errorf("hybrid vector phase: %w", err)
	}

	// Phase 1b: Text search (when enabled) — fuse with RRF.
	// Text search is best-effort: if the backend doesn't support it
	// (e.g. Postgres), we fall back to vector-only results.
	if opts.TextSearch {
		textOpts := opts.Vector
		textOpts.Threshold = 0 // BM25 scores are on a different scale; don't filter.
		textResults, textErr := e.SearchText(ctx, textOpts)
		if textErr == nil && len(textResults) > 0 {
			vectorResults = fuseByRRF(vectorResults, textResults, opts.rrfK())
		}
		// Cap fused results to the requested limit so graph expansion
		// doesn't do 2x work from the combined result set.
		if opts.Vector.Limit > 0 && len(vectorResults) > opts.Vector.Limit {
			vectorResults = vectorResults[:opts.Vector.Limit]
		}
	}

	// Track seen entries to deduplicate.
	seen := make(map[string]bool)
	var allResults []Result

	for _, r := range vectorResults {
		seen[r.Entry.ID.String()] = true
		allResults = append(allResults, r)
	}

	// Phase 2: Graph expansion from each vector result.
	edgeFilter := storage.EdgeFilter{}
	edgeTypeSet := make(map[model.EdgeType]bool, len(opts.ExpandEdgeTypes))
	for _, et := range opts.ExpandEdgeTypes {
		edgeTypeSet[et] = true
	}
	if len(opts.ExpandEdgeTypes) == 1 {
		edgeFilter.Type = opts.ExpandEdgeTypes[0]
	}

	for _, vectorResult := range vectorResults {
		expanded, err := e.expandFromEntry(
			ctx,
			vectorResult.Entry.ID,
			opts.ExpandDepth,
			opts.ExpandDirection,
			edgeFilter,
			edgeTypeSet,
			seen,
		)
		if err != nil {
			return nil, fmt.Errorf("hybrid expand from %s: %w", vectorResult.Entry.ID, err)
		}
		allResults = append(allResults, expanded...)
	}

	// Enrich with conflict information (vector results already have it, but
	// the expansion results do not).
	if err := e.enrichConflicts(ctx, allResults); err != nil {
		return nil, fmt.Errorf("enrich conflicts: %w", err)
	}

	return allResults, nil
}

// expandFromEntry performs BFS expansion from a single entry, adding newly
// discovered entries as expansion results.
func (e *Engine) expandFromEntry(
	ctx context.Context,
	startID model.ID,
	maxDepth int,
	direction GraphDirection,
	edgeFilter storage.EdgeFilter,
	edgeTypeSet map[model.EdgeType]bool,
	seen map[string]bool,
) ([]Result, error) {

	type bfsItem struct {
		entryID model.ID
		depth   int
	}

	var results []Result

	queue := []bfsItem{{entryID: startID, depth: 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		var edges []model.Edge

		if direction == Outgoing || direction == Both {
			out, err := e.edges.EdgesFrom(ctx, current.entryID, edgeFilter)
			if err != nil {
				return nil, err
			}
			edges = append(edges, out...)
		}

		if direction == Incoming || direction == Both {
			in, err := e.edges.EdgesTo(ctx, current.entryID, edgeFilter)
			if err != nil {
				return nil, err
			}
			edges = append(edges, in...)
		}

		for _, edge := range edges {
			if len(edgeTypeSet) > 0 && !edgeTypeSet[edge.Type] {
				continue
			}

			neighborID := edge.ToID
			if edge.ToID == current.entryID {
				neighborID = edge.FromID
			}

			if seen[neighborID.String()] {
				continue
			}
			seen[neighborID.String()] = true

			neighborEntry, err := e.entries.Get(ctx, neighborID)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("get entry %s: %w", neighborID, err)
			}

			results = append(results, Result{
				Entry:    *neighborEntry,
				Score:    edge.EffectiveWeight(),
				Reach:    ReachExpansion,
				Depth:    current.depth + 1,
				EdgePath: []model.Edge{edge},
			})

			queue = append(queue, bfsItem{
				entryID: neighborID,
				depth:   current.depth + 1,
			})
		}
	}

	return results, nil
}

// fuseByRRF merges vector and text results using Reciprocal Rank Fusion.
//
// RRF scores each entry as: score = Σ 1/(k + rank) across the result lists
// where rank is the 1-based position in each list. This uses only rank
// position, so incompatible score scales (cosine vs BM25) are irrelevant.
//
// For entries found by both methods, the Reach field is set to the method
// that ranked the entry higher. The original per-method scores are discarded
// in favor of the fused RRF score.
func fuseByRRF(vectorResults, textResults []Result, k int) []Result {
	type fusedEntry struct {
		result   Result
		rrfScore float64
		bestRank int // lowest rank across lists (for Reach attribution)
	}

	fused := make(map[model.ID]*fusedEntry, len(vectorResults)+len(textResults))

	// Score vector results by rank position.
	for rank, r := range vectorResults {
		score := 1.0 / float64(k+rank+1) // rank is 0-based, RRF uses 1-based
		fused[r.Entry.ID] = &fusedEntry{
			result:   r,
			rrfScore: score,
			bestRank: rank,
		}
	}

	// Score text results by rank position and merge.
	for rank, r := range textResults {
		score := 1.0 / float64(k+rank+1)
		if fe, exists := fused[r.Entry.ID]; exists {
			fe.rrfScore += score
			// Attribute Reach to whichever method ranked higher; ties favor vector.
			if rank < fe.bestRank {
				fe.bestRank = rank
				fe.result.Reach = ReachText
			}
		} else {
			r.Reach = ReachText
			fused[r.Entry.ID] = &fusedEntry{
				result:   r,
				rrfScore: score,
				bestRank: rank,
			}
		}
	}

	// Collect and sort by fused score.
	results := make([]Result, 0, len(fused))
	for _, fe := range fused {
		fe.result.Score = fe.rrfScore
		results = append(results, fe.result)
	}
	sortResultsByScore(results)
	return results
}
