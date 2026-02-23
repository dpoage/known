package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"
	"time"

	"github.com/dpoage/known/query"
)

func runSearch(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	scope := fs.String("scope", "", "scope to search within (default: auto from cwd)")
	limit := fs.Int("limit", 10, "maximum number of results")
	threshold := fs.Float64("threshold", 0.3, "minimum similarity score (0-1)")
	recency := fs.Float64("recency", 0, "recency weight (0=pure similarity, 1=pure recency)")
	hybrid := fs.Bool("hybrid", false, "use hybrid vector+graph search")
	expandDepth := fs.Int("expand-depth", 1, "graph expansion depth for hybrid search")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
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

	app.Printer.PrintResults(results)
	return nil
}
