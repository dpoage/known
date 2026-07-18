package query

import (
	"context"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// ReinforceConfig controls the reinforcement algorithm.
type ReinforceConfig struct {
	// BoostAmount is the default amount to increase edge weight per
	// action-after-recall signal. Per-action overrides take precedence when set.
	// Deprecated: prefer per-action amounts (ShowBoost, UpdateBoost, LinkBoost).
	BoostAmount float64

	// ShowBoost is the weight increment when an entry is *shown* after recall.
	// Viewing is weak signal; 0.02 reflects that it may just be incidental
	// inspection rather than confirmation of relevance.
	ShowBoost float64

	// UpdateBoost is the weight increment when an entry is *updated* after
	// recall. Updating signals strong relevance — the agent read and then acted.
	UpdateBoost float64

	// LinkBoost is the weight increment when an entry is *linked* after recall.
	// Linking is equally strong as updating: the agent intentionally connected
	// the recalled entry to new information.
	LinkBoost float64

	// DecayFactor is multiplied against each edge's weight during every
	// reinforcement cycle: newWeight = weight*DecayFactor + boost.
	// Must be in (0, 1]. Zero disables decay (treated as 1.0).
	//
	// Both decay and boost are applied together to every edge adjacent to an
	// acted-on entry in a cycle. Edges whose endpoints are never acted on are
	// NOT visited and do NOT decay under this model (write-time decay only).
	// This is intentional: a full-graph scan each cycle would be O(E) with no
	// benefit since unvisited edges never appear in retrieval results anyway.
	//
	// Equilibrium: the recurrence w(t+1) = d*w(t) + b converges to b/(1-d).
	// To keep saturated edges BELOW MaxWeight, choose constants so that
	// b/(1-d) < MaxWeight. The default config satisfies this: with the largest
	// boost b=0.05 and d=0.90, the equilibrium is 0.05/0.10 = 0.5 < 1.0.
	//
	// Floor: decayed weight is clamped to MinWeight so edges never reach zero.
	DecayFactor float64

	// MinWeight is the lower bound for any edge weight after decay.
	// Default 0.01 keeps edges alive but weak.
	MinWeight float64

	// MaxWeight is the maximum allowed edge weight.
	MaxWeight float64
}

// DefaultReinforceConfig returns the default reinforcement configuration.
//
// Constants are chosen so that the decay equilibrium b/(1-d) is STRICTLY
// below MaxWeight (1.0) for every action type:
//   - show:        0.02 / (1 - 0.90) = 0.20
//   - update/link: 0.05 / (1 - 0.90) = 0.50
//
// This means a frequently-boosted edge converges to a stable value (0.2 or
// 0.5) rather than pinning to the 1.0 cap, keeping weight meaningful as a
// signal. MaxWeight is still enforced as a hard ceiling (e.g. for manually
// set weights or legacy data), but the default cycle will not saturate it.
func DefaultReinforceConfig() ReinforceConfig {
	return ReinforceConfig{
		ShowBoost:   0.02,
		UpdateBoost: 0.05,
		LinkBoost:   0.05,
		DecayFactor: 0.90,
		MinWeight:   0.01,
		MaxWeight:   1.0,
	}
}

// boostForAction returns the configured boost for the given event type,
// falling back to cfg.BoostAmount for unrecognised or zero-configured types.
func (cfg ReinforceConfig) boostForAction(evType model.EventType) float64 {
	switch evType {
	case model.EventShow:
		if cfg.ShowBoost > 0 {
			return cfg.ShowBoost
		}
	case model.EventUpdate:
		if cfg.UpdateBoost > 0 {
			return cfg.UpdateBoost
		}
	case model.EventLink:
		if cfg.LinkBoost > 0 {
			return cfg.LinkBoost
		}
	}
	// Fallback: use BoostAmount (covers EventDelete and legacy callers that
	// only set BoostAmount).
	return cfg.BoostAmount
}

func (cfg ReinforceConfig) decayFactor() float64 {
	if cfg.DecayFactor <= 0 || cfg.DecayFactor > 1 {
		return 1.0 // disabled
	}
	return cfg.DecayFactor
}

func (cfg ReinforceConfig) minWeight() float64 {
	if cfg.MinWeight <= 0 {
		return 0.01
	}
	return cfg.MinWeight
}

// ReinforceResult summarizes what was done during reinforcement.
type ReinforceResult struct {
	SessionsProcessed int
	EdgesBoosted      int
}

// TxFunc wraps a function in a database transaction. The function receives
// a context that carries the transaction; all repo operations using that
// context participate in the same transaction.
type TxFunc func(ctx context.Context, fn func(ctx context.Context) error) error

// Reinforce analyzes unprocessed sessions for action-after-recall patterns
// and boosts connected edge weights. Each session's edge boosts and
// mark-as-processed are wrapped in a transaction to prevent double-boosting
// on partial failure. Sessions are idempotent via session_reinforcements table.
func (e *Engine) Reinforce(ctx context.Context, sessions storage.SessionRepo, withTx TxFunc, cfg ReinforceConfig) (*ReinforceResult, error) {
	unprocessed, err := sessions.ListUnprocessedSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unprocessed sessions: %w", err)
	}

	result := &ReinforceResult{}

	for _, session := range unprocessed {
		var boosted int
		err := withTx(ctx, func(txCtx context.Context) error {
			var err error
			boosted, err = e.processSession(txCtx, sessions, session.ID, cfg)
			if err != nil {
				return fmt.Errorf("process session %s: %w", session.ID, err)
			}
			if err := sessions.MarkProcessed(txCtx, session.ID); err != nil {
				return fmt.Errorf("mark processed %s: %w", session.ID, err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		result.EdgesBoosted += boosted
		result.SessionsProcessed++
	}

	return result, nil
}

// processSession analyzes a single session's event timeline for
// action-after-recall patterns and boosts relevant edge weights.
//
// Error contract: on any Update failure the function returns immediately with
// the count of edges successfully boosted before the failure and the error.
// The caller (Reinforce) wraps this in a transaction, so the partial updates
// are rolled back — boosted will be zero from the caller's perspective on
// error. This is the "fail-fast with count" contract.
func (e *Engine) processSession(ctx context.Context, sessions storage.SessionRepo, sessionID model.ID, cfg ReinforceConfig) (int, error) {
	events, err := sessions.ListEvents(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("list events: %w", err)
	}

	// Find action-after-recall patterns.
	actedEvents := findActedEvents(events)
	if len(actedEvents) == 0 {
		return 0, nil
	}

	// Collect acted entry IDs and their strongest action type (prefer update/link
	// over show when an entry was acted on multiple times in one session).
	type actionInfo struct {
		entryID    model.ID
		actionType model.EventType
	}
	bestAction := make(map[model.ID]model.EventType)
	for _, ae := range actedEvents {
		for _, id := range ae.EntryIDs {
			existing, ok := bestAction[id]
			if !ok {
				bestAction[id] = ae.EventType
				continue
			}
			// Prefer stronger signals: update/link > show > other.
			if actionStrength(ae.EventType) > actionStrength(existing) {
				bestAction[id] = ae.EventType
			}
		}
	}

	decay := cfg.decayFactor()
	minW := cfg.minWeight()

	boosted := 0
	seen := make(map[string]bool)

	for entryID, actionType := range bestAction {
		boost := cfg.boostForAction(actionType)

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

			// Apply decay first, then add boost. This means a boosted edge
			// gets: weight*decay + boost. An unboosted edge just gets: weight*decay.
			// Since we only touch acted-on entries' edges here, "unboosted" edges
			// don't appear in this loop — decay of non-adjacent edges happens on
			// their own reinforcement cycles. This is intentional: edges only decay
			// when their nodes are visited, avoiding a full-graph scan each cycle.
			newWeight := edge.EffectiveWeight()*decay + boost
			if newWeight > cfg.MaxWeight {
				newWeight = cfg.MaxWeight
			}
			if newWeight < minW {
				newWeight = minW
			}

			edge.Weight = &newWeight
			if err := e.edges.Update(ctx, &edge); err != nil {
				// Return the count of edges boosted BEFORE this failure.
				// The transaction in Reinforce will roll all of them back,
				// so the net applied count from the caller's view is 0.
				return boosted, fmt.Errorf("update edge %s: %w", edge.ID, err)
			}
			boosted++
		}
	}

	return boosted, nil
}

// actionStrength returns a numeric priority for ordering action types.
// Higher = stronger signal.
func actionStrength(evType model.EventType) int {
	switch evType {
	case model.EventUpdate, model.EventLink:
		return 2
	case model.EventShow:
		return 1
	default:
		return 0
	}
}

// findActedEvents scans an event timeline for action-after-recall patterns.
// Returns events that were acted on after a recall event (preserving event
// type so callers can apply differentiated boosts).
func findActedEvents(events []model.SessionEvent) []model.SessionEvent {
	var result []model.SessionEvent
	hadRecall := false

	for _, ev := range events {
		switch ev.EventType {
		case model.EventRecall, model.EventSearch:
			hadRecall = true
		case model.EventShow, model.EventUpdate, model.EventLink, model.EventDelete:
			if hadRecall && len(ev.EntryIDs) > 0 {
				result = append(result, ev)
			}
		}
	}

	return result
}

// findActedEntries scans an event timeline for action-after-recall patterns.
// Returns a set of entry IDs that were acted on after a recall event.
// Deprecated: use findActedEvents for differentiated boost support.
func findActedEntries(events []model.SessionEvent) map[model.ID]bool {
	result := make(map[model.ID]bool)
	for _, ev := range findActedEvents(events) {
		for _, id := range ev.EntryIDs {
			result[id] = true
		}
	}
	return result
}
