package query

import (
	"context"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// ReinforceConfig controls the reinforcement algorithm.
type ReinforceConfig struct {
	// BoostAmount is the amount to increase edge weight per action-after-recall signal.
	BoostAmount float64

	// MaxWeight is the maximum allowed edge weight.
	MaxWeight float64
}

// DefaultReinforceConfig returns the default reinforcement configuration.
func DefaultReinforceConfig() ReinforceConfig {
	return ReinforceConfig{
		BoostAmount: 0.05,
		MaxWeight:   1.0,
	}
}

// ReinforceResult summarizes what was done during reinforcement.
type ReinforceResult struct {
	SessionsProcessed int
	EdgesBoosted      int
}

// Reinforce analyzes unprocessed sessions for action-after-recall patterns
// and boosts connected edge weights. Sessions are marked as processed
// afterward (idempotent via session_reinforcements table).
func (e *Engine) Reinforce(ctx context.Context, sessions storage.SessionRepo, cfg ReinforceConfig) (*ReinforceResult, error) {
	unprocessed, err := sessions.ListUnprocessedSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unprocessed sessions: %w", err)
	}

	result := &ReinforceResult{}

	for _, session := range unprocessed {
		boosted, err := e.processSession(ctx, sessions, session.ID, cfg)
		if err != nil {
			return nil, fmt.Errorf("process session %s: %w", session.ID, err)
		}
		result.EdgesBoosted += boosted
		result.SessionsProcessed++

		if err := sessions.MarkProcessed(ctx, session.ID); err != nil {
			return nil, fmt.Errorf("mark processed %s: %w", session.ID, err)
		}
	}

	return result, nil
}

// processSession analyzes a single session's event timeline for
// action-after-recall patterns and boosts relevant edge weights.
func (e *Engine) processSession(ctx context.Context, sessions storage.SessionRepo, sessionID model.ID, cfg ReinforceConfig) (int, error) {
	events, err := sessions.ListEvents(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("list events: %w", err)
	}

	// Find action-after-recall patterns.
	// A recall establishes a "context" of retrieved entries.
	// Subsequent show/update/link/delete actions on specific entries
	// are signals that those entries (and their connections) are valuable.
	actedEntryIDs := findActedEntries(events)
	if len(actedEntryIDs) == 0 {
		return 0, nil
	}

	// Find edges connected to acted-on entries and boost their weights.
	boosted := 0
	seen := make(map[string]bool)

	for entryID := range actedEntryIDs {
		outEdges, err := e.edges.EdgesFrom(ctx, entryID, storage.EdgeFilter{})
		if err != nil {
			return boosted, fmt.Errorf("edges from %s: %w", entryID, err)
		}
		inEdges, err := e.edges.EdgesTo(ctx, entryID, storage.EdgeFilter{})
		if err != nil {
			return boosted, fmt.Errorf("edges to %s: %w", entryID, err)
		}

		allEdges := append(outEdges, inEdges...)
		for _, edge := range allEdges {
			if seen[edge.ID.String()] {
				continue
			}
			seen[edge.ID.String()] = true

			newWeight := edge.EffectiveWeight() + cfg.BoostAmount
			if newWeight > cfg.MaxWeight {
				newWeight = cfg.MaxWeight
			}

			edge.Weight = &newWeight
			if err := e.edges.Update(ctx, &edge); err != nil {
				return boosted, fmt.Errorf("update edge %s: %w", edge.ID, err)
			}
			boosted++
		}
	}

	return boosted, nil
}

// findActedEntries scans an event timeline for action-after-recall patterns.
// Returns a set of entry IDs that were acted on after a recall event.
func findActedEntries(events []model.SessionEvent) map[model.ID]bool {
	result := make(map[model.ID]bool)
	hadRecall := false

	for _, ev := range events {
		switch ev.EventType {
		case model.EventRecall, model.EventSearch:
			hadRecall = true
		case model.EventShow, model.EventUpdate, model.EventLink, model.EventDelete:
			if hadRecall {
				for _, id := range ev.EntryIDs {
					result[id] = true
				}
			}
		}
	}

	return result
}
