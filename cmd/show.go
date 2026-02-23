package cmd

import (
	"context"
	"fmt"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// runShow implements the "known show" subcommand.
//
// Usage:
//
//	known show <id>                — show a single entry by ID
//	known show --scope <path>      — show all entries in scope with full detail
//
// Displays the full entry details plus all edges (both outgoing and incoming)
// and conflict status.
func runShow(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	scope := fs.String("scope", "", "show all entries in this scope and descendants")
	limit := fs.Int("limit", 20, "maximum entries for scope-based mode")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// If a positional ID arg is provided, show a single entry (existing behavior).
	if fs.NArg() > 0 {
		return showByID(ctx, app, fs.Arg(0))
	}

	// If --scope is provided, show all entries in that scope.
	if *scope != "" {
		return showByScope(ctx, app, *scope, *limit)
	}

	return fmt.Errorf("usage: known show <id>\n       known show --scope <path>\n\nProvide an entry ID or --scope to browse entries.")
}

// showByID displays a single entry by its ULID with full detail and edges.
func showByID(ctx context.Context, app *App, idStr string) error {
	id, err := model.ParseID(idStr)
	if err != nil {
		return fmt.Errorf("invalid entry ID: %w", err)
	}

	// Fetch the entry.
	entry, err := app.Entries.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get entry: %w", err)
	}

	// Fetch edges in both directions.
	outgoing, err := app.Edges.EdgesFrom(ctx, id, storage.EdgeFilter{})
	if err != nil {
		return fmt.Errorf("get outgoing edges: %w", err)
	}

	incoming, err := app.Edges.EdgesTo(ctx, id, storage.EdgeFilter{})
	if err != nil {
		return fmt.Errorf("get incoming edges: %w", err)
	}

	// Check for conflicts.
	conflicts, err := app.Edges.FindConflicts(ctx, id)
	if err != nil {
		return fmt.Errorf("find conflicts: %w", err)
	}
	if len(conflicts) > 0 {
		conflictIDs := make([]model.ID, len(conflicts))
		for i, c := range conflicts {
			conflictIDs[i] = c.ID
		}
		entry.ConflictsWith = conflictIDs
		entry.ResolutionStatus = model.ResolutionUnresolved
	}

	// Print entry.
	app.Printer.PrintEntry(*entry)

	// Print relationships based on output mode.
	if app.Printer.json {
		// In JSON mode, output a combined structure.
		type showResult struct {
			Entry    model.Entry  `json:"entry"`
			Outgoing []model.Edge `json:"outgoing_edges"`
			Incoming []model.Edge `json:"incoming_edges"`
		}
		app.Printer.printJSON(showResult{
			Entry:    *entry,
			Outgoing: outgoing,
			Incoming: incoming,
		})
	} else if !app.Printer.quiet {
		// Human mode gets extra labels.
		if len(outgoing) > 0 {
			fmt.Fprintln(app.Printer.w)
			fmt.Fprintf(app.Printer.w, "Outgoing edges (%d):\n", len(outgoing))
			app.Printer.PrintEdges(outgoing)
		}
		if len(incoming) > 0 {
			fmt.Fprintln(app.Printer.w)
			fmt.Fprintf(app.Printer.w, "Incoming edges (%d):\n", len(incoming))
			app.Printer.PrintEdges(incoming)
		}
		if len(outgoing) == 0 && len(incoming) == 0 {
			fmt.Fprintln(app.Printer.w)
			fmt.Fprintln(app.Printer.w, "No relationships.")
		}
	}
	// Quiet mode already printed just the ID from PrintEntry.

	return nil
}

// showByScope lists all entries in the given scope with full entry detail
// (same format as single-entry show, repeated for each entry).
func showByScope(ctx context.Context, app *App, scope string, limit int) error {
	filter := storage.EntryFilter{
		ScopePrefix: scope,
		Limit:       limit,
	}

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return fmt.Errorf("list entries: %w", err)
	}

	if app.Printer.json {
		app.Printer.printJSON(entries)
		return nil
	}

	if len(entries) == 0 {
		if !app.Printer.quiet {
			fmt.Fprintln(app.Printer.w, "No entries found.")
		}
		return nil
	}

	for _, e := range entries {
		app.Printer.PrintEntry(e)
	}

	if !app.Printer.quiet {
		fmt.Fprintf(app.Printer.w, "(%d entries)\n", len(entries))
	}

	return nil
}
