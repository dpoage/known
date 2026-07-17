package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// resolveEntry resolves a user-provided argument to an entry ID.
// If arg is a valid ULID, it is used directly. Otherwise, arg is treated as a
// content query using resolveEntryConfident.
//
// On a single confident match, the entry is returned directly.
// On multiple matches without a clear winner, the candidates are printed and an error is returned.
// On no matches, an error is returned.
func resolveEntry(ctx context.Context, app *App, arg string) (model.ID, error) {
	return resolveEntryConfident(ctx, app, arg)
}

// resolveEntryConfident resolves a content query to a single entry ID using a
// confidence-first strategy:
//
//  1. ULID fast path: if arg parses as a ULID, use it directly.
//  2. Exact content match: scan the text-search results for an entry whose
//     content exactly equals arg (case-insensitive). Resolves immediately.
//  3. Top-1 dominance: if semantic search is available, require the top result
//     to have score ≥ 0.80 AND a margin of ≥ 0.10 over the second result.
//     If so, resolve immediately — no matter how many other entries exist.
//  4. Text-search fallback: same dominance rule applied to substring matches.
//  5. Ambiguity: if no confident winner, list up to 5 candidates and exit
//     nonzero with a fix-stating message.
//
// This ensures that in a store with 10+ entries, an exact or near-exact content
// query still resolves to a single entry without requiring the agent to provide
// a ULID.
func resolveEntryConfident(ctx context.Context, app *App, arg string) (model.ID, error) {
	// Fast path: valid ULID.
	if id, err := model.ParseID(arg); err == nil {
		return id, nil
	}

	// Try semantic search first (embedder may not be configured).
	semanticResults, semErr := searchSemanticScored(ctx, app, arg)
	if semErr == nil && len(semanticResults) > 0 {
		// Exact content match anywhere in semantic results.
		for _, r := range semanticResults {
			if strings.EqualFold(r.Entry.Content, arg) {
				if !app.Printer.quiet {
					fmt.Fprintf(app.Printer.w, "Resolved %q → %s\n", arg, formatCandidate(r.Entry))
				}
				return r.Entry.ID, nil
			}
		}

		// Top-1 dominance: score ≥ threshold AND margin over #2.
		const (
			minScore = 0.80
			minMargin = 0.10
		)
		top := semanticResults[0]
		if top.Score >= minScore {
			var secondScore float64
			if len(semanticResults) > 1 {
				secondScore = semanticResults[1].Score
			}
			if top.Score-secondScore >= minMargin {
				if !app.Printer.quiet {
					fmt.Fprintf(app.Printer.w, "Resolved %q → %s\n", arg, formatCandidate(top.Entry))
				}
				return top.Entry.ID, nil
			}
		}

		// Ambiguous semantic results.
		limit := len(semanticResults)
		if limit > 5 {
			limit = 5
		}
		fmt.Fprintf(app.Printer.w, "Multiple entries match %q:\n", arg)
		for i, r := range semanticResults[:limit] {
			fmt.Fprintf(app.Printer.w, "  %d. %s\n", i+1, formatCandidate(r.Entry))
		}
		fmt.Fprintln(app.Printer.w, "Use a more specific query or provide the full ID.")
		return model.ID{}, fmt.Errorf("ambiguous query %q: %d matches", arg, len(semanticResults))
	}

	// Semantic unavailable or returned nothing; fall back to substring search.
	textCandidates, err := searchByText(ctx, app, arg)
	if err != nil {
		return model.ID{}, fmt.Errorf("search entries: %w", err)
	}

	switch len(textCandidates) {
	case 0:
		return model.ID{}, fmt.Errorf("no entries matching %q", arg)
	case 1:
		if !app.Printer.quiet {
			fmt.Fprintf(app.Printer.w, "Resolved %q → %s\n", arg, formatCandidate(textCandidates[0]))
		}
		return textCandidates[0].ID, nil
	default:
		// Exact content match in text results.
		for _, e := range textCandidates {
			if strings.EqualFold(e.Content, arg) {
				if !app.Printer.quiet {
					fmt.Fprintf(app.Printer.w, "Resolved %q → %s\n", arg, formatCandidate(e))
				}
				return e.ID, nil
			}
		}

		// Ambiguous: list candidates.
		limit := len(textCandidates)
		if limit > 5 {
			limit = 5
		}
		fmt.Fprintf(app.Printer.w, "Multiple entries match %q:\n", arg)
		for i, e := range textCandidates[:limit] {
			fmt.Fprintf(app.Printer.w, "  %d. %s\n", i+1, formatCandidate(e))
		}
		fmt.Fprintln(app.Printer.w, "Use a more specific query or provide the full ID.")
		return model.ID{}, fmt.Errorf("ambiguous query %q: %d matches", arg, len(textCandidates))
	}
}

// searchSemanticScored returns scored results from vector search so callers can
// apply dominance rules. It lazily initializes the embedder if not already present.
//
// NOTE: This mutates app.Embedder and app.Engine as a side effect. Safe for the
// short-lived CLI process, but will need revisiting for long-lived contexts.
func searchSemanticScored(ctx context.Context, app *App, q string) ([]query.Result, error) {
	if app.Embedder == nil {
		embedCfg := embed.LoadConfig()
		embedder, err := embed.NewEmbedder(embedCfg)
		if err != nil {
			return nil, fmt.Errorf("embedder unavailable: %w", err)
		}
		app.Embedder = embedder
		app.Engine = query.New(app.Entries, app.Edges, embedder)
	}

	scope := resolveScope(app)

	// Fetch enough results to evaluate dominance: top-6 covers our needs.
	results, err := app.Engine.SearchVector(ctx, query.VectorOptions{
		Text:  q,
		Scope: scope,
		Limit: 6,
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// searchEntries tries semantic search first (lazily initializing the embedder),
// falling back to substring search if the embedder is unavailable or returns no results.
// Used by callers that just want a flat list without scored dominance.
func searchEntries(ctx context.Context, app *App, q string) ([]model.Entry, error) {
	results, err := searchSemanticScored(ctx, app, q)
	if err == nil && len(results) > 0 {
		entries := make([]model.Entry, len(results))
		for i, r := range results {
			entries[i] = r.Entry
		}
		return entries, nil
	}
	// Fall back to text search only when the embedder is unavailable (not configured,
	// missing API key, etc.) or returned no results. Propagate real errors.
	if err != nil && !isEmbedderUnavailable(err) {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	return searchByText(ctx, app, q)
}

// isEmbedderUnavailable returns true when the error indicates the embedder
// could not be initialized (missing config, API key, etc.) as opposed to a
// transient or infrastructure failure.
func isEmbedderUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "embedder unavailable")
}

// searchByText finds entries whose title or content contains the query substring.
// Used as a fallback when semantic search is unavailable.
func searchByText(ctx context.Context, app *App, q string) ([]model.Entry, error) {
	scope := resolveScope(app)

	filter := storage.EntryFilter{
		ScopePrefix: scope,
		Limit:       200,
	}

	entries, err := app.Entries.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(q)
	var matches []model.Entry
	for _, e := range entries {
		titleMatch := e.Title != "" && strings.Contains(strings.ToLower(e.Title), queryLower)
		contentMatch := strings.Contains(strings.ToLower(e.Content), queryLower)
		if titleMatch || contentMatch {
			matches = append(matches, e)
			if len(matches) >= 10 {
				break
			}
		}
	}

	return matches, nil
}

// resolveScope returns the scope to use for entry resolution.
// Falls back to "root" if no scope is derived (SearchVector requires non-empty scope).
func resolveScope(app *App) string {
	scope := app.Config.DefaultScope
	if scope == "" {
		return model.RootScope
	}
	return scope
}

// formatCandidate returns a short display string for an entry.
func formatCandidate(e model.Entry) string {
	label := e.Title
	if label == "" {
		label = e.Content
		if len(label) > 60 {
			label = label[:57] + "..."
		}
	}
	return fmt.Sprintf("%s  %s  [%s]", e.ID, label, e.Scope)
}
