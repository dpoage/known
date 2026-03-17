package cmd

import (
	"context"
	"fmt"
	flag "github.com/spf13/pflag"
	"strconv"
	"strings"

	"github.com/dpoage/known/model"
)

func runLink(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	edgeType := fs.String("type", "related-to", "edge type (e.g. depends-on, elaborates, contradicts)")
	weight := fs.Float64("weight", -1, "edge weight (0.0-1.0, omit to leave unset)")
	meta := fs.String("meta", "", "metadata as key=value pairs (comma-separated)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 2 {
		types := make([]string, len(model.PredefinedEdgeTypes()))
		for i, t := range model.PredefinedEdgeTypes() {
			types[i] = string(t)
		}
		return fmt.Errorf("usage: known link <from-id> <to-id> --type <edge-type>\n\n" +
			"Create a directed edge: from → to.\n" +
			"Arguments can be entry IDs (ULIDs) or content queries.\n\n" +
			"Edge types: %s\n\n" +
			"Examples:\n" +
			"  known link 01ABC 01DEF --type elaborates\n" +
			"  known link 01ABC 01DEF                    # defaults to related-to",
			strings.Join(types, ", "))
	}

	fromID, err := resolveEntry(ctx, app, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("from: %w", err)
	}

	toID, err := resolveEntry(ctx, app, fs.Arg(1))
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}

	// Validate both entries exist.
	if _, err := app.Entries.Get(ctx, fromID); err != nil {
		return fmt.Errorf("from entry %s: %w", fromID, err)
	}
	if _, err := app.Entries.Get(ctx, toID); err != nil {
		return fmt.Errorf("to entry %s: %w", toID, err)
	}

	// Validate edge type.
	et := model.EdgeType(*edgeType)
	if err := et.Validate(); err != nil {
		types := make([]string, len(model.PredefinedEdgeTypes()))
		for i, t := range model.PredefinedEdgeTypes() {
			types[i] = string(t)
		}
		return fmt.Errorf("edge type: %w (valid types: %s)", err, strings.Join(types, ", "))
	}

	edge := model.NewEdge(fromID, toID, et)

	if *weight >= 0 {
		edge = edge.WithWeight(*weight)
	}

	if *meta != "" {
		m, err := parseMeta(*meta)
		if err != nil {
			return fmt.Errorf("parse meta: %w", err)
		}
		edge = edge.WithMeta(m)
	}

	if err := app.Edges.Create(ctx, &edge); err != nil {
		return fmt.Errorf("create edge: %w", err)
	}

	app.Printer.PrintEdge(edge)
	app.Printer.PrintMessage("Edge created.")
	return nil
}

// parseMeta parses a comma-separated list of key=value pairs into Metadata.
// Values are stored as strings unless they look like numbers.
func parseMeta(s string) (model.Metadata, error) {
	m := make(model.Metadata)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid key=value pair: %q", pair)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)

		// Try to parse as number for nicer JSON output.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m[k] = f
		} else {
			m[k] = v
		}
	}
	return m, nil
}
