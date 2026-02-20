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
// First, it runs a vector search using the provided options. Then, for each
// vector result, it expands outward along graph edges up to the specified
// depth. The final result set contains both direct vector matches and entries
// discovered through graph expansion, deduplicated by entry ID.
func (e *Engine) SearchHybrid(ctx context.Context, opts HybridOptions) ([]Result, error) {
	if opts.ExpandDepth <= 0 {
		opts.ExpandDepth = 1
	}

	// Phase 1: Vector search.
	vectorResults, err := e.SearchVector(ctx, opts.Vector)
	if err != nil {
		return nil, fmt.Errorf("hybrid vector phase: %w", err)
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
