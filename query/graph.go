package query

import (
	"context"
	"errors"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// Traverse performs a breadth-first graph traversal from the starting entry,
// following edges up to the specified maximum depth.
//
// It supports directional traversal (outgoing, incoming, or both), edge type
// filtering, and returns results annotated with depth and the edge path
// used to reach each entry.
func (e *Engine) Traverse(ctx context.Context, opts GraphOptions) ([]Result, error) {
	if opts.StartID.IsZero() {
		return nil, fmt.Errorf("start ID is required")
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 1
	}

	edgeFilter := storage.EdgeFilter{}

	// Build the set of allowed edge types for quick lookup.
	edgeTypeSet := make(map[model.EdgeType]bool, len(opts.EdgeTypes))
	for _, et := range opts.EdgeTypes {
		edgeTypeSet[et] = true
	}

	// If exactly one edge type, use the storage filter for efficiency.
	if len(opts.EdgeTypes) == 1 {
		edgeFilter.Type = opts.EdgeTypes[0]
	}

	// visited tracks entry IDs we have already processed.
	visited := make(map[string]bool)

	// bfsItem holds traversal state for each entry in the BFS queue.
	type bfsItem struct {
		entryID  model.ID
		depth    int
		edgePath []model.Edge
	}

	var results []Result

	// Optionally include the starting entry.
	if opts.IncludeStart {
		startEntry, err := e.entries.Get(ctx, opts.StartID)
		if err != nil {
			return nil, fmt.Errorf("get start entry: %w", err)
		}
		results = append(results, Result{
			Entry: *startEntry,
			Reach: ReachTraversal,
			Depth: 0,
		})
	}

	visited[opts.StartID.String()] = true

	queue := []bfsItem{{entryID: opts.StartID, depth: 0, edgePath: nil}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= opts.MaxDepth {
			continue
		}

		// Collect edges based on direction.
		var edges []model.Edge

		if opts.Direction == Outgoing || opts.Direction == Both {
			out, err := e.edges.EdgesFrom(ctx, current.entryID, edgeFilter)
			if err != nil {
				return nil, fmt.Errorf("edges from %s: %w", current.entryID, err)
			}
			edges = append(edges, out...)
		}

		if opts.Direction == Incoming || opts.Direction == Both {
			in, err := e.edges.EdgesTo(ctx, current.entryID, edgeFilter)
			if err != nil {
				return nil, fmt.Errorf("edges to %s: %w", current.entryID, err)
			}
			edges = append(edges, in...)
		}

		for _, edge := range edges {
			// Apply edge type filter if types were specified.
			if len(edgeTypeSet) > 0 && !edgeTypeSet[edge.Type] {
				continue
			}

			// Determine the neighbor (the other end of the edge).
			neighborID := edge.ToID
			if edge.ToID == current.entryID {
				neighborID = edge.FromID
			}

			if visited[neighborID.String()] {
				continue
			}
			visited[neighborID.String()] = true

			neighborEntry, err := e.entries.Get(ctx, neighborID)
			if errors.Is(err, storage.ErrNotFound) {
				// Entry may have been deleted; skip.
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("get entry %s: %w", neighborID, err)
			}

			newPath := make([]model.Edge, len(current.edgePath)+1)
			copy(newPath, current.edgePath)
			newPath[len(current.edgePath)] = edge

			result := Result{
				Entry:    *neighborEntry,
				Reach:    ReachTraversal,
				Depth:    current.depth + 1,
				EdgePath: newPath,
			}

			results = append(results, result)

			queue = append(queue, bfsItem{
				entryID:  neighborID,
				depth:    current.depth + 1,
				edgePath: newPath,
			})
		}
	}

	// Enrich with conflict information.
	if err := e.enrichConflicts(ctx, results); err != nil {
		return nil, fmt.Errorf("enrich conflicts: %w", err)
	}

	return results, nil
}

// FindPath finds the shortest path between two entries using BFS.
// It returns the results along the path (excluding the start entry) and the
// edges connecting them. Returns nil results if no path exists.
func (e *Engine) FindPath(ctx context.Context, fromID, toID model.ID, maxDepth int) ([]Result, error) {
	if fromID.IsZero() || toID.IsZero() {
		return nil, fmt.Errorf("from and to IDs are required")
	}
	if maxDepth <= 0 {
		maxDepth = 5
	}

	type bfsItem struct {
		entryID  model.ID
		edgePath []model.Edge
		depth    int
	}

	visited := make(map[string]bool)
	visited[fromID.String()] = true

	queue := []bfsItem{{entryID: fromID, depth: 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		// Follow edges in both directions for path finding.
		var edges []model.Edge

		out, err := e.edges.EdgesFrom(ctx, current.entryID, storage.EdgeFilter{})
		if err != nil {
			return nil, fmt.Errorf("edges from %s: %w", current.entryID, err)
		}
		edges = append(edges, out...)

		in, err := e.edges.EdgesTo(ctx, current.entryID, storage.EdgeFilter{})
		if err != nil {
			return nil, fmt.Errorf("edges to %s: %w", current.entryID, err)
		}
		edges = append(edges, in...)

		for _, edge := range edges {
			neighborID := edge.ToID
			if edge.ToID == current.entryID {
				neighborID = edge.FromID
			}

			if visited[neighborID.String()] {
				continue
			}
			visited[neighborID.String()] = true

			newPath := make([]model.Edge, len(current.edgePath)+1)
			copy(newPath, current.edgePath)
			newPath[len(current.edgePath)] = edge

			// Found the target.
			if neighborID == toID {
				return e.buildPathResults(ctx, fromID, newPath)
			}

			queue = append(queue, bfsItem{
				entryID:  neighborID,
				depth:    current.depth + 1,
				edgePath: newPath,
			})
		}
	}

	// No path found.
	return nil, nil
}

// buildPathResults constructs Result entries for each hop in a path.
// startID is the ID of the entry where the path search originated, used to
// correctly seed the seen set regardless of edge direction.
func (e *Engine) buildPathResults(ctx context.Context, startID model.ID, edges []model.Edge) ([]Result, error) {
	results := make([]Result, 0, len(edges))

	// Track which entry IDs we have already seen to handle the path correctly.
	// Seed with the start node so we only emit newly discovered entries.
	seen := make(map[string]bool)
	seen[startID.String()] = true

	for i, edge := range edges {
		// For each edge, pick the end we haven't visited yet.
		var targetID model.ID
		if !seen[edge.FromID.String()] {
			targetID = edge.FromID
		} else {
			targetID = edge.ToID
		}

		if seen[targetID.String()] {
			continue
		}
		seen[targetID.String()] = true

		entry, err := e.entries.Get(ctx, targetID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get path entry %s: %w", targetID, err)
		}

		pathSoFar := make([]model.Edge, i+1)
		copy(pathSoFar, edges[:i+1])

		results = append(results, Result{
			Entry:    *entry,
			Reach:    ReachTraversal,
			Depth:    i + 1,
			EdgePath: pathSoFar,
		})
	}

	return results, nil
}
