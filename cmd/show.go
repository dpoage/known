package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// runShow implements the "known show" subcommand.
//
// Usage: known show <id>
//
// Displays the full entry details plus all edges (both outgoing and incoming)
// and conflict status.
func runShow(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return fmt.Errorf("entry ID is required\nUsage: known show <id>")
	}

	id, err := model.ParseID(fs.Arg(0))
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
