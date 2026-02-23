package cmd

import (
	"context"
	"fmt"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// runList implements the "known list" subcommand — browse entries by scope,
// source type, and confidence level without requiring a search query or ULID.
//
// Usage: known list [--scope <path>] [--source-type <type>] [--confidence <level>] [--limit <n>] [--json]
func runList(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	scope := fs.String("scope", "", "filter to this scope and descendants")
	sourceType := fs.String("source-type", "", "filter by source type (file, url, conversation, manual)")
	confidence := fs.String("confidence", "", "filter by confidence level (verified, inferred, uncertain)")
	limit := fs.Int("limit", 20, "maximum number of results")
	jsonOut := fs.Bool("json", false, "output as JSON array")

	if err := fs.Parse(args); err != nil {
		return err
	}

	*scope = app.Config.QualifyScope(*scope)

	filter := storage.EntryFilter{
		ScopePrefix:    *scope,
		SourceType:     model.SourceType(*sourceType),
		ConfidenceLevel: model.ConfidenceLevel(*confidence),
		Limit:          *limit,
	}

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return fmt.Errorf("list entries: %w", err)
	}

	if *jsonOut || app.Printer.json {
		app.Printer.printJSON(entries)
		return nil
	}

	if len(entries) == 0 {
		if !app.Printer.quiet {
			fmt.Fprintln(app.Printer.w, "No entries found.")
		}
		return nil
	}

	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(app.Printer.w)
		}
		fmt.Fprintf(app.Printer.w, "ID:         %s\n", e.ID)
		if e.Title != "" {
			fmt.Fprintf(app.Printer.w, "Title:      %s\n", e.Title)
		} else {
			fmt.Fprintf(app.Printer.w, "Content:    %s\n", truncate(e.Content, 100))
		}
		fmt.Fprintf(app.Printer.w, "Scope:      %s\n", e.Scope)
		fmt.Fprintf(app.Printer.w, "Confidence: %s\n", e.Confidence.Level)
		fmt.Fprintf(app.Printer.w, "Source:     %s\n", e.Source.Type)
		fmt.Fprintf(app.Printer.w, "Age:        %s\n", formatAge(e.CreatedAt))
	}

	if !app.Printer.quiet {
		fmt.Fprintf(app.Printer.w, "\n(%d entries)\n", len(entries))
	}

	return nil
}

// formatAge returns a human-friendly relative time string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	default:
		months := int(d.Hours() / 24 / 30)
		if months == 0 {
			months = 1
		}
		if months == 1 {
			return "1mo ago"
		}
		return fmt.Sprintf("%dmo ago", months)
	}
}
