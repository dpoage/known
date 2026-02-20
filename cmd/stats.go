package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/dpoage/known/storage"
)

// statsData holds the computed statistics for JSON output.
type statsData struct {
	EntryCount int    `json:"entry_count"`
	EdgeCount  int    `json:"edge_count"`
	ScopeCount int    `json:"scope_count"`
	Scope      string `json:"scope,omitempty"`
}

func runStats(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	scope := fs.String("scope", "", "filter stats to this scope (and descendants)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	var filter storage.EntryFilter
	if *scope != "" {
		filter.ScopePrefix = *scope
	}

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return fmt.Errorf("list entries: %w", err)
	}

	// Count edges for the relevant entries.
	edgeCount := 0
	for _, entry := range entries {
		from, err := app.Edges.EdgesFrom(ctx, entry.ID, storage.EdgeFilter{})
		if err != nil {
			return fmt.Errorf("edges from %s: %w", entry.ID, err)
		}
		edgeCount += len(from)
	}

	scopes, err := app.Scopes.List(ctx)
	if err != nil {
		return fmt.Errorf("list scopes: %w", err)
	}

	// If scope is filtered, count only matching scopes.
	scopeCount := len(scopes)
	if *scope != "" {
		scopeCount = 0
		for _, s := range scopes {
			if s.Path == *scope || (len(s.Path) > len(*scope) && s.Path[:len(*scope)+1] == *scope+".") {
				scopeCount++
			}
		}
	}

	data := statsData{
		EntryCount: len(entries),
		EdgeCount:  edgeCount,
		ScopeCount: scopeCount,
		Scope:      *scope,
	}

	if app.Printer.json {
		app.Printer.printJSON(data)
		return nil
	}

	if *scope != "" {
		fmt.Fprintf(app.Printer.w, "Scope:   %s\n", *scope)
	}
	fmt.Fprintf(app.Printer.w, "Entries: %d\n", data.EntryCount)
	fmt.Fprintf(app.Printer.w, "Edges:   %d\n", data.EdgeCount)
	fmt.Fprintf(app.Printer.w, "Scopes:  %d\n", data.ScopeCount)

	return nil
}
