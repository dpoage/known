package cmd

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

func runRelated(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("related", flag.ContinueOnError)
	depth := fs.Int("depth", 2, "maximum traversal depth")
	direction := fs.String("direction", "both", "traversal direction: out, in, both")
	edgeTypes := fs.String("edge-type", "", "comma-separated edge types to follow")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known related <id> [flags]\n\nFind related entries via graph traversal.")
	}

	startID, err := model.ParseID(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid ID: %w", err)
	}

	dir, err := parseDirection(*direction)
	if err != nil {
		return err
	}

	opts := query.GraphOptions{
		StartID:      startID,
		Direction:    dir,
		MaxDepth:     *depth,
		IncludeStart: false,
	}

	if *edgeTypes != "" {
		for _, t := range strings.Split(*edgeTypes, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				opts.EdgeTypes = append(opts.EdgeTypes, model.EdgeType(t))
			}
		}
	}

	results, err := app.Engine.Traverse(ctx, opts)
	if err != nil {
		return fmt.Errorf("traverse: %w", err)
	}

	app.Printer.PrintResults(results)
	return nil
}

// parseDirection converts a string direction flag to a query.GraphDirection.
func parseDirection(s string) (query.GraphDirection, error) {
	switch strings.ToLower(s) {
	case "out", "outgoing":
		return query.Outgoing, nil
	case "in", "incoming":
		return query.Incoming, nil
	case "both":
		return query.Both, nil
	default:
		return 0, fmt.Errorf("invalid direction %q: must be out, in, or both", s)
	}
}
