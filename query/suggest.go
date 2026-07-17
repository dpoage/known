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
// The source entry itself is excluded from results. Entries already linked
// to/from the source are NOT excluded — deduplication at the accept layer is
// acceptable since the edge repo will error on exact duplicates anyway.
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

	// Fetch k+1 so we can discard the source entry itself if it appears.
	fetchLimit := k + 1

	var simResults []storage.SimilarityResult
	var err error

	if entry.HasEmbedding() {
		// Vector path: search by the entry's own embedding vector directly,
		// bypassing a second embed call. SearchSimilar accepts the raw vector.
		simResults, err = e.entries.SearchSimilar(ctx, entry.Embedding, scope, storage.Cosine, fetchLimit)
		if err != nil {
			return nil, err
		}
	} else {
		// No embedding: try full-text search as a best-effort fallback.
		// SearchText is optional on the repo; a nil/empty result is fine.
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
		suggestions = append(suggestions, LinkSuggestion{
			Entry:    sr.Entry,
			Score:    distanceToScore(sr.Distance, storage.Cosine),
			EdgeType: model.EdgeRelatedTo,
		})
		if len(suggestions) == k {
			break
		}
	}

	return suggestions, nil
}
