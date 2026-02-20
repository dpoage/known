package query

import (
	"context"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// FindConflicts returns all entries that have a "contradicts" edge involving
// the given entry. Results include the conflicting entries with their conflict
// metadata populated.
func (e *Engine) FindConflicts(ctx context.Context, entryID model.ID) ([]Result, error) {
	if entryID.IsZero() {
		return nil, fmt.Errorf("entry ID is required")
	}

	conflicts, err := e.edges.FindConflicts(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("find conflicts: %w", err)
	}

	results := make([]Result, 0, len(conflicts))
	for _, c := range conflicts {
		results = append(results, Result{
			Entry:       c,
			Reach:       ReachTraversal,
			Depth:       1,
			HasConflict: true,
			Conflicts:   []model.ID{entryID},
		})
	}

	return results, nil
}

// DetectAllConflicts scans entries in the given scope for any that participate
// in contradicts edges and returns them grouped as conflict pairs.
func (e *Engine) DetectAllConflicts(ctx context.Context, scope string) ([]ConflictPair, error) {
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}

	// List all entries in the scope.
	entries, err := e.entries.List(ctx, storage.EntryFilter{
		ScopePrefix: scope,
	})
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}

	// For each entry, check for contradiction edges.
	// Track seen edge IDs to avoid duplicating pairs.
	seenEdges := make(map[string]bool)
	var pairs []ConflictPair

	for _, entry := range entries {
		// Get outgoing contradicts edges.
		contradicts, err := e.edges.EdgesFrom(ctx, entry.ID, storage.EdgeFilter{
			Type: model.EdgeContradicts,
		})
		if err != nil {
			return nil, fmt.Errorf("edges from %s: %w", entry.ID, err)
		}

		for _, edge := range contradicts {
			if seenEdges[edge.ID.String()] {
				continue
			}
			seenEdges[edge.ID.String()] = true

			other, err := e.entries.Get(ctx, edge.ToID)
			if err != nil {
				continue
			}

			pairs = append(pairs, ConflictPair{
				EntryA: entry,
				EntryB: *other,
				Edge:   edge,
			})
		}
	}

	return pairs, nil
}
