package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"
	"time"

	"github.com/dpoage/known/query"
)

// runRecall implements the "known recall" subcommand — an LLM-optimized
// retrieval command that returns clean, context-ready text.
//
// Usage: known recall <query> [--scope <path>]
//
// Unlike "search", recall uses hardcoded parameters tuned for LLM consumption
// and outputs plain text without IDs, scores, timestamps, or JSON structure.
func runRecall(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	scope := fs.String("scope", "", "scope to search within (default: auto from cwd)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known recall <query> [--scope <path>]\n\nRetrieve knowledge optimized for LLM context.")
	}

	queryText := fs.Arg(0)

	opts := query.HybridOptions{
		Vector: query.VectorOptions{
			Text:            queryText,
			Scope:           *scope,
			Limit:           5,
			Threshold:       0.3,
			RecencyWeight:   0.1,
			RecencyHalfLife: 7 * 24 * time.Hour,
		},
		ExpandDepth:     1,
		ExpandDirection: query.Both,
	}

	results, err := app.Engine.SearchHybrid(ctx, opts)
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}

	app.Printer.PrintRecallResults(results)
	return nil
}
