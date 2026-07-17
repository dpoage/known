package query

import (
	"context"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// LinkSuggestion is a candidate edge proposed by the suggestion engine.
type LinkSuggestion struct {
	Entry    model.Entry    // the candidate neighbor entry
	Score    float64        // similarity score (0–1, higher = more similar)
	EdgeType model.EdgeType // proposed edge type
}

// SuggestLinks returns the top-k entries most similar to the given entry,
// as candidate link targets. The entry must have an embedding; without one
// only text-based fallback is attempted (which may return fewer results).
//
// Edge type defaults to related-to for all suggestions. A more specific type
// (contradicts, supersedes, elaborates, depends-on) would require either
// human judgment or expensive NLI inference — neither belongs in a one-shot
// add workflow. Callers may override the type when accepting a suggestion via
// `known link accept`.
//
// The source entry itself and any entries already linked to/from the source
// are excluded. This makes repeat calls idempotent: already-linked entries
// never re-appear as suggestions.
//
// SuggestLinks is intentionally non-fatal: callers should surface the error
// as a warning and continue. An empty slice with no error means no similar
// entries were found.
func (e *Engine) SuggestLinks(ctx context.Context, entry model.Entry, k int) ([]LinkSuggestion, error) {
	if k <= 0 {
		k = 3
	}

	scope := entry.Scope
	if scope == "" {
		scope = model.RootScope
	}

	// Build the set of IDs already linked to/from this entry so we can skip them.
	linked, err := e.linkedIDs(ctx, entry.ID)
	if err != nil {
		return nil, err
	}

	// Fetch more than k to account for self and already-linked entries in results.
	fetchLimit := k + 1 + len(linked)

	var simResults []storage.SimilarityResult

	if entry.HasEmbedding() {
		// Vector path: search using the entry's own stored embedding vector,
		// bypassing a redundant embed call.
		simResults, err = e.entries.SearchSimilar(ctx, entry.Embedding, scope, storage.Cosine, fetchLimit)
		if err != nil {
			return nil, err
		}
	} else {
		// No embedding: try full-text search as a best-effort fallback.
		if entry.Content == "" {
			return nil, nil
		}
		simResults, err = e.entries.SearchText(ctx, entry.Content, scope, fetchLimit)
		if err != nil {
			return nil, err
		}
	}

	suggestions := make([]LinkSuggestion, 0, k)
	for _, sr := range simResults {
		if sr.Entry.ID == entry.ID {
			continue // exclude self
		}
		if linked[sr.Entry.ID] {
			continue // exclude already-linked neighbors
		}

		var score float64
		if entry.HasEmbedding() {
			score = distanceToScore(sr.Distance, storage.Cosine)
		} else {
			// BM25 rank from FTS5 is negative; convert to 0–1 score.
			score = bm25ToScore(sr.Distance)
		}

		suggestions = append(suggestions, LinkSuggestion{
			Entry:    sr.Entry,
			Score:    score,
			EdgeType: model.EdgeRelatedTo,
		})
		if len(suggestions) == k {
			break
		}
	}

	return suggestions, nil
}

// linkedIDs returns the set of entry IDs that are already connected to entryID
// by any edge in either direction.
func (e *Engine) linkedIDs(ctx context.Context, entryID model.ID) (map[model.ID]bool, error) {
	out, err := e.edges.EdgesFrom(ctx, entryID, storage.EdgeFilter{})
	if err != nil {
		return nil, err
	}
	in, err := e.edges.EdgesTo(ctx, entryID, storage.EdgeFilter{})
	if err != nil {
		return nil, err
	}

	linked := make(map[model.ID]bool, len(out)+len(in))
	for _, edge := range out {
		linked[edge.ToID] = true
	}
	for _, edge := range in {
		linked[edge.FromID] = true
	}
	return linked, nil
}
