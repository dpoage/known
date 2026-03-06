package cmd

import (
	"context"
	"fmt"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

func runSearch(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	scope := fs.String("scope", "", "scope to search within (default: auto from cwd)")
	limit := fs.Int("limit", app.Config.RecallLimit, "maximum number of results")
	threshold := fs.Float64("threshold", app.Config.SearchThreshold, "minimum similarity score (0-1)")
	recency := fs.Float64("recency", 0, "recency weight (0=pure similarity, 1=pure recency)")
	var labelFlags multiFlag
	fs.Var(&labelFlags, "label", "filter by label (repeatable, post-filter)")
	hybrid := fs.Bool("hybrid", false, "use hybrid vector+graph search")
	expandDepth := fs.Int("expand-depth", 1, "graph expansion depth for hybrid search")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	} else {
		*scope = app.Config.QualifyScope(*scope)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known search <query text> [flags]\n\nSearch entries by semantic similarity.")
	}

	queryText := fs.Arg(0)

	if *hybrid {
		opts := query.HybridOptions{
			Vector: query.VectorOptions{
				Text:          queryText,
				Scope:         *scope,
				Limit:         *limit,
				Threshold:     *threshold,
				RecencyWeight: *recency,
			},
			ExpandDepth:     *expandDepth,
			ExpandDirection: query.Both,
		}
		results, err := app.Engine.SearchHybrid(ctx, opts)
		if err != nil {
			return fmt.Errorf("hybrid search: %w", err)
		}
		results = filterResultsByLabels(results, labelFlags)
		app.Printer.PrintResults(results)
		return nil
	}

	opts := query.VectorOptions{
		Text:            queryText,
		Scope:           *scope,
		Limit:           *limit,
		Threshold:       *threshold,
		RecencyWeight:   *recency,
		RecencyHalfLife: 7 * 24 * time.Hour,
	}

	results, err := app.Engine.SearchVector(ctx, opts)
	if err != nil {
		return fmt.Errorf("vector search: %w", err)
	}

	results = filterResultsByLabels(results, labelFlags)
	app.Printer.PrintResults(results)
	return nil
}

// filterResultsByLabels post-filters query results to include only entries
// that have all the specified labels.
func filterResultsByLabels(results []query.Result, labels []string) []query.Result {
	if len(labels) == 0 {
		return results
	}
	var filtered []query.Result
	for _, r := range results {
		if entryHasAllLabels(r.Entry, labels) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func entryHasAllLabels(e model.Entry, labels []string) bool {
	labelSet := make(map[string]bool, len(e.Labels))
	for _, l := range e.Labels {
		labelSet[l] = true
	}
	for _, l := range labels {
		if !labelSet[l] {
			return false
		}
	}
	return true
}
