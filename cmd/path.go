package cmd

import (
	"context"
	"fmt"

	flag "github.com/spf13/pflag"
)

func runPath(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("path", flag.ContinueOnError)
	maxDepth := fs.Int("max-depth", 5, "maximum search depth")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 2 {
		return fmt.Errorf("usage: known path <from> <to> [flags]\n\nFind the shortest path between two entries. Arguments can be IDs or content queries.")
	}

	fromID, err := resolveEntry(ctx, app, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("from: %w", err)
	}

	toID, err := resolveEntry(ctx, app, fs.Arg(1))
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}

	results, err := app.Engine.FindPath(ctx, fromID, toID, *maxDepth)
	if err != nil {
		return fmt.Errorf("find path: %w", err)
	}

	if results == nil {
		app.Printer.PrintMessage("No path found between %s and %s (max depth %d).", fromID, toID, *maxDepth)
		return nil
	}

	app.Printer.PrintResults(results)
	return nil
}
