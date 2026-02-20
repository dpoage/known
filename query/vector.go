package query

import (
	"context"
	"fmt"
	"strings"
)

// SearchVector performs a vector similarity search over the knowledge graph.
//
// It embeds the query text, searches for similar entries within the specified
// scope, optionally applies recency weighting, and filters out entries whose
// content matches any exclusion patterns.
func (e *Engine) SearchVector(ctx context.Context, opts VectorOptions) ([]Result, error) {
	if opts.Text == "" {
		return nil, fmt.Errorf("query text is required")
	}
	if opts.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	// Embed the query text.
	queryVec, err := e.embedder.Embed(ctx, opts.Text)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Request more results than needed to account for exclusion filtering.
	fetchLimit := opts.Limit
	if len(opts.ExcludeContent) > 0 {
		fetchLimit = opts.Limit * 3
		if fetchLimit < 30 {
			fetchLimit = 30
		}
	}

	simResults, err := e.entries.SearchSimilar(ctx, queryVec, opts.Scope, opts.Metric, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("search similar: %w", err)
	}

	halfLife := opts.RecencyHalfLife
	if halfLife <= 0 {
		halfLife = 7 * 24 * 3600_000_000_000 // 7 days in nanoseconds as Duration
	}

	var results []Result
	for _, sr := range simResults {
		// Apply exclusion filter.
		if shouldExclude(sr.Entry.Content, opts.ExcludeContent) {
			continue
		}

		similarity := distanceToScore(sr.Distance)

		// Apply recency weighting if configured.
		score := similarity
		if opts.RecencyWeight > 0 {
			freshness := freshnessScore(sr.Entry.CreatedAt, halfLife)
			score = blendScore(similarity, freshness, opts.RecencyWeight)
		}

		// Apply threshold filter.
		if opts.Threshold > 0 && score < opts.Threshold {
			continue
		}

		results = append(results, Result{
			Entry:    sr.Entry,
			Score:    score,
			Distance: sr.Distance,
			Reach:    ReachDirect,
			Depth:    0,
		})

		if len(results) >= opts.Limit {
			break
		}
	}

	// Re-sort by score if recency weighting was applied, since the reweighting
	// may have changed the order from the distance-sorted storage results.
	if opts.RecencyWeight > 0 && len(results) > 1 {
		sortResultsByScore(results)
	}

	// Enrich results with conflict information.
	if err := e.enrichConflicts(ctx, results); err != nil {
		return nil, fmt.Errorf("enrich conflicts: %w", err)
	}

	return results, nil
}

// shouldExclude returns true if the content contains any of the exclusion patterns.
func shouldExclude(content string, excludes []string) bool {
	lower := strings.ToLower(content)
	for _, ex := range excludes {
		if strings.Contains(lower, strings.ToLower(ex)) {
			return true
		}
	}
	return false
}

// sortResultsByScore sorts results in descending order by score (highest first).
func sortResultsByScore(results []Result) {
	// Simple insertion sort; result sets are small (typically <= 100).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
