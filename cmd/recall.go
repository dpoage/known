package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// validProvenanceLevels lists the accepted --provenance values.
var validProvenanceLevels = map[model.ProvenanceLevel]bool{
	model.ProvenanceVerified:  true,
	model.ProvenanceInferred:  true,
	model.ProvenanceUncertain: true,
}

// validSourceTypes lists the accepted --source values.
var validSourceTypes = map[model.SourceType]bool{
	model.SourceFile:         true,
	model.SourceURL:          true,
	model.SourceConversation: true,
	model.SourceManual:       true,
}

// runRecall implements the "known recall" subcommand — an LLM-optimized
// retrieval command that returns clean, context-ready text.
//
// Usage:
//
//	known recall <query> [--scope <path>]       — semantic search
//	known recall --scope <path>                 — list all entries in scope
//
// Flags allow tuning the search parameters while preserving LLM-friendly output.
// Entry IDs are always included so agents can act on results (link, update, delete).
func runRecall(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	scope := fs.String("scope", "", "scope to search within (default: auto from cwd)")
	var labelFlags multiFlag
	fs.Var(&labelFlags, "label", "filter by label (repeatable)")
	limit := fs.Int("limit", app.Config.RecallLimit, "maximum number of results")
	threshold := fs.Float64("threshold", app.Config.SearchThreshold, "minimum similarity score (0-1)")
	recency := fs.Float64("recency", app.Config.RecallRecency, "recency weight (0=pure similarity, 1=pure recency)")
	expandDepth := fs.Int("expand-depth", app.Config.RecallExpandDepth, "graph expansion depth (hops from each vector result)")
	provenance := fs.String("provenance", "", "filter by provenance level (verified, inferred, uncertain)")
	source := fs.String("source", "", "filter by source type (file, url, conversation, manual)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate provenance if provided.
	if *provenance != "" {
		if !validProvenanceLevels[model.ProvenanceLevel(*provenance)] {
			return fmt.Errorf("invalid --provenance %q: must be one of verified, inferred, uncertain", *provenance)
		}
	}

	// Validate source if provided.
	if *source != "" {
		if !validSourceTypes[model.SourceType(*source)] {
			return fmt.Errorf("invalid --source %q: must be one of file, url, conversation, manual", *source)
		}
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	} else {
		*scope = app.Config.QualifyScope(*scope)
	}

	// If no query arg, require --scope for scope-based listing.
	if fs.NArg() < 1 {
		if *scope == "" {
			return fmt.Errorf("usage: known recall <query> [--scope <path>]\n       known recall --scope <path>\n\nProvide a query for semantic search, or --scope to list entries in a scope.")
		}
		return recallByScope(ctx, app, *scope, *limit, labelFlags,
			model.ProvenanceLevel(*provenance), model.SourceType(*source))
	}

	queryText := fs.Arg(0)

	opts := query.HybridOptions{
		Vector: query.VectorOptions{
			Text:            queryText,
			Scope:           *scope,
			Limit:           *limit,
			Threshold:       *threshold,
			RecencyWeight:   *recency,
			RecencyHalfLife: 7 * 24 * time.Hour,
		},
		ExpandDepth:     *expandDepth,
		ExpandDirection: query.Both,
	}

	results, err := app.Engine.SearchHybrid(ctx, opts)
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}

	results = filterResultsByLabels(results, labelFlags)
	results = filterResultsByProvenance(results, model.ProvenanceLevel(*provenance))
	results = filterResultsBySource(results, model.SourceType(*source))
	app.Printer.PrintRecallResults(results)
	return nil
}

// recallByScope lists all entries in the given scope using the recall plain-text format.
func recallByScope(ctx context.Context, app *App, scope string, limit int, labels []string,
	provenance model.ProvenanceLevel, source model.SourceType) error {

	filter := storage.EntryFilter{
		ScopePrefix:     scope,
		Labels:          labels,
		ProvenanceLevel: provenance,
		SourceType:      source,
		Limit:           limit,
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
			meta = fmt.Sprintf("[%s: %s] (%s, source: %s, %s) {%s}",
				e.Scope, e.Title, e.Provenance.Level, e.Source.Reference, e.Freshness.FreshnessLabel(), e.ID)
		} else {
			meta = fmt.Sprintf("[%s] (%s, source: %s, %s) {%s}",
				e.Scope, e.Provenance.Level, e.Source.Reference, e.Freshness.FreshnessLabel(), e.ID)
		}
		if len(e.Labels) > 0 {
			meta += fmt.Sprintf(" [labels: %s]", strings.Join(e.Labels, ", "))
		}
		fmt.Fprintln(app.Printer.w, meta)
		fmt.Fprintln(app.Printer.w, e.Content)
	}

	return nil
}

// filterResultsByProvenance post-filters query results to include only entries
// with the specified provenance level. Returns results unchanged if level is empty.
func filterResultsByProvenance(results []query.Result, level model.ProvenanceLevel) []query.Result {
	if level == "" {
		return results
	}
	var filtered []query.Result
	for _, r := range results {
		if r.Entry.Provenance.Level == level {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// filterResultsBySource post-filters query results to include only entries
// with the specified source type. Returns results unchanged if sourceType is empty.
func filterResultsBySource(results []query.Result, sourceType model.SourceType) []query.Result {
	if sourceType == "" {
		return results
	}
	var filtered []query.Result
	for _, r := range results {
		if r.Entry.Source.Type == sourceType {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
