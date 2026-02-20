package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// exportEntry is the export format that includes an entry and its edges.
type exportEntry struct {
	Entry model.Entry  `json:"entry"`
	Edges []model.Edge `json:"edges,omitempty"`
}

func runExport(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "json", "output format: json or jsonl")
	scope := fs.String("scope", "", "filter to this scope (and descendants)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *format != "json" && *format != "jsonl" {
		return fmt.Errorf("invalid format %q: must be json or jsonl", *format)
	}

	var filter storage.EntryFilter
	if *scope != "" {
		filter.ScopePrefix = *scope
	}
	filter.IncludeExpired = true // export everything

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return fmt.Errorf("list entries: %w", err)
	}

	// Build export entries with edges.
	exports := make([]exportEntry, 0, len(entries))
	for _, entry := range entries {
		edges, err := app.Edges.EdgesFrom(ctx, entry.ID, storage.EdgeFilter{})
		if err != nil {
			return fmt.Errorf("edges from %s: %w", entry.ID, err)
		}
		exports = append(exports, exportEntry{
			Entry: entry,
			Edges: edges,
		})
	}

	w := app.Printer.w

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(exports); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

	case "jsonl":
		enc := json.NewEncoder(w)
		for _, e := range exports {
			if err := enc.Encode(e); err != nil {
				return fmt.Errorf("encode jsonl: %w", err)
			}
		}
	}

	return nil
}
