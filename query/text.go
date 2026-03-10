package query

import (
	"context"
	"fmt"
)

// SearchText performs a full-text search over the knowledge graph using FTS5.
//
// It searches for entries matching the query text within the specified scope,
// converts BM25 ranks to similarity scores, and optionally applies recency
// weighting and threshold filtering.
// The Metric field of VectorOptions is ignored for text search.
func (e *Engine) SearchText(ctx context.Context, opts VectorOptions) ([]Result, error) {
	if opts.Text == "" {
		return nil, fmt.Errorf("query text is required")
	}
	if opts.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	// Request more results than needed to account for exclusion filtering.
	fetchLimit := opts.Limit
	if len(opts.ExcludeContent) > 0 {
		fetchLimit = opts.Limit * 3
		if fetchLimit < 30 {
			fetchLimit = 30
		}
	}

	simResults, err := e.entries.SearchText(ctx, opts.Text, opts.Scope, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("search text: %w", err)
	}

	halfLife := opts.RecencyHalfLife
	if halfLife <= 0 {
		halfLife = 7 * 24 * 3600_000_000_000 // 7 days
	}

	var results []Result
	for _, sr := range simResults {
		// Apply exclusion filter.
		if shouldExclude(sr.Entry.Content, opts.ExcludeContent) {
			continue
		}

		score := bm25ToScore(sr.Distance)

		// Apply recency weighting if configured.
		if opts.RecencyWeight > 0 {
			freshness := freshnessScore(sr.Entry.CreatedAt, halfLife)
			score = blendScore(score, freshness, opts.RecencyWeight)
		}

		// Apply threshold filter.
		if opts.Threshold > 0 && score < opts.Threshold {
			continue
		}

		results = append(results, Result{
			Entry:    sr.Entry,
			Score:    score,
			Distance: sr.Distance,
			Reach:    ReachText,
			Depth:    0,
		})
	}

	// Sort by score if recency weighting was applied, since reweighting
	// can change order relative to the BM25-sorted storage results.
	if opts.RecencyWeight > 0 && len(results) > 1 {
		sortResultsByScore(results)
	}

	// Apply limit after sorting.
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	// Enrich results with conflict information.
	if err := e.enrichConflicts(ctx, results); err != nil {
		return nil, fmt.Errorf("enrich conflicts: %w", err)
	}

	return results, nil
}
