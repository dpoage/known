package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
)

func runLink(ctx context.Context, app *App, args []string) error {
	// Dispatch to accept subcommand if first arg is "accept".
	if len(args) > 0 && args[0] == "accept" {
		return runLinkAccept(ctx, app, args[1:])
	}

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
		return fmt.Errorf("usage: known link <from> <to> [--type <edge-type>]\n\n" +
			"Create a directed edge: from → to.\n" +
			"Arguments can be entry IDs (ULIDs) or content queries.\n\n" +
			"Subcommands:\n" +
			"  known link accept <entry-ref> [--all | 1 2 ...]   accept suggested links\n\n" +
			"Edge types: %s\n\n" +
			"Examples:\n" +
			"  known link 'renderer architecture' 'graphics pipeline'\n" +
			"  known link 01ABC 01DEF --type elaborates\n" +
			"  known link accept 'renderer architecture' 1 2",
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

// runLinkAccept implements `known link accept <entry-ref> [--all | 1 2 ...]`.
//
// It recomputes the top-K suggestions for the given entry (stateless — no
// suggestion store required) and creates edges for the accepted indices.
// Accepted indices are 1-based to match the display in `known add` output.
//
// Examples:
//
//	known link accept 'renderer architecture' --all
//	known link accept 'renderer architecture' 1 3
//	known link accept 01KJ... 2
func runLinkAccept(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("link accept", flag.ContinueOnError)
	all := fs.Bool("all", false, "accept all suggestions")
	edgeType := fs.String("type", "", "override edge type for accepted links (default: use suggested type)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known link accept <entry-ref> [--all | 1 2 ...]\n\n" +
			"Accept suggested links for an entry. <entry-ref> can be a ULID or content query.\n" +
			"Indices are 1-based and match the numbers shown in 'known add' output.\n\n" +
			"Examples:\n" +
			"  known link accept 'renderer architecture' --all\n" +
			"  known link accept 'renderer architecture' 1 3\n" +
			"  known link accept 01KJ... 2")
	}

	// Resolve the source entry.
	entryID, err := resolveEntry(ctx, app, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("entry: %w", err)
	}

	entry, err := app.Entries.Get(ctx, entryID)
	if err != nil {
		return fmt.Errorf("get entry %s: %w", entryID, err)
	}

	// Ensure the engine is ready (lazy-init embedder if needed).
	if err := ensureEngine(ctx, app); err != nil {
		return fmt.Errorf("init engine: %w", err)
	}

	const defaultK = 5
	suggestions, err := app.Engine.SuggestLinks(ctx, *entry, defaultK)
	if err != nil {
		return fmt.Errorf("compute suggestions: %w", err)
	}
	if len(suggestions) == 0 {
		app.Printer.PrintMessage("No link suggestions found for this entry.")
		return nil
	}

	// Determine which indices to accept.
	var indices []int
	if *all {
		indices = make([]int, len(suggestions))
		for i := range suggestions {
			indices[i] = i
		}
	} else {
		// Remaining args are 1-based indices.
		idxArgs := fs.Args()[1:]
		if len(idxArgs) == 0 {
			// Print available suggestions and prompt.
			app.Printer.PrintMessage("Suggestions for %s:", entry.ID)
			for i, s := range suggestions {
				app.Printer.PrintMessage("  %d. [%s] %s", i+1, s.EdgeType, formatCandidate(s.Entry))
			}
			app.Printer.PrintMessage("Specify indices or --all: known link accept '%s' 1 2", entry.Content[:min(40, len(entry.Content))])
			return fmt.Errorf("no indices specified; use --all or provide indices (1-%d)", len(suggestions))
		}
		for _, arg := range idxArgs {
			n, err := strconv.Atoi(arg)
			if err != nil || n < 1 || n > len(suggestions) {
				return fmt.Errorf("invalid index %q: expected a number between 1 and %d", arg, len(suggestions))
			}
			indices = append(indices, n-1) // convert to 0-based
		}
	}

	// Override edge type if requested.
	var overrideType model.EdgeType
	if *edgeType != "" {
		overrideType = model.EdgeType(*edgeType)
		if err := overrideType.Validate(); err != nil {
			types := make([]string, len(model.PredefinedEdgeTypes()))
			for i, t := range model.PredefinedEdgeTypes() {
				types[i] = string(t)
			}
			return fmt.Errorf("edge type: %w (valid types: %s)", err, strings.Join(types, ", "))
		}
	}

	// Create edges for accepted suggestions.
	created := 0
	for _, idx := range indices {
		s := suggestions[idx]
		et := s.EdgeType
		if overrideType != "" {
			et = overrideType
		}
		edge := model.NewEdge(entryID, s.Entry.ID, et)
		if err := app.Edges.Create(ctx, &edge); err != nil {
			// Non-fatal: report but continue with remaining indices.
			fmt.Fprintf(app.Stderr, "warning: link %s → %s: %v\n", entryID, s.Entry.ID, err)
			continue
		}
		app.Printer.PrintMessage("Linked → %s [%s]", formatCandidate(s.Entry), et)
		created++
	}

	if created == 0 {
		return fmt.Errorf("no edges created (all failed or already existed)")
	}
	app.Printer.PrintMessage("%d edge(s) created.", created)
	return nil
}

// ensureEngine initializes app.Engine and app.Embedder if not already set.
// This is the same lazy-init pattern used by searchSemantic in resolve.go.
func ensureEngine(ctx context.Context, app *App) error {
	if app.Engine != nil {
		return nil
	}
	return fmt.Errorf("embedder unavailable: not configured (set KNOWN_EMBEDDER or run 'known init')")
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

