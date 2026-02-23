package cmd

import (
	"context"
	"fmt"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// runRecall implements the "known recall" subcommand — an LLM-optimized
// retrieval command that returns clean, context-ready text.
//
// Usage:
//
//	known recall <query> [--scope <path>]       — semantic search
//	known recall --scope <path>                 — list all entries in scope
//
// Unlike "search", recall uses hardcoded parameters tuned for LLM consumption
// and outputs plain text without IDs, scores, timestamps, or JSON structure.
func runRecall(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	scope := fs.String("scope", "", "scope to search within (default: auto from cwd)")
	limit := fs.Int("limit", 20, "maximum number of results")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	}

	// If no query arg, require --scope for scope-based listing.
	if fs.NArg() < 1 {
		if *scope == "" {
			return fmt.Errorf("usage: known recall <query> [--scope <path>]\n       known recall --scope <path>\n\nProvide a query for semantic search, or --scope to list entries in a scope.")
		}
		return recallByScope(ctx, app, *scope, *limit)
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

// recallByScope lists all entries in the given scope using the recall plain-text format.
func recallByScope(ctx context.Context, app *App, scope string, limit int) error {
	filter := storage.EntryFilter{
		ScopePrefix: scope,
		Limit:       limit,
	}

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintln(app.Printer.w, "No matching knowledge found.")
		return nil
	}

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(app.Printer.w)
		}
		var meta string
		if e.Title != "" {
			meta = fmt.Sprintf("[%s: %s] (%s, source: %s)",
				e.Scope, e.Title, e.Confidence.Level, e.Source.Reference)
		} else {
			meta = fmt.Sprintf("[%s] (%s, source: %s)",
				e.Scope, e.Confidence.Level, e.Source.Reference)
		}
		fmt.Fprintln(app.Printer.w, meta)
		fmt.Fprintln(app.Printer.w, e.Content)
	}

	return nil
}
