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
// content query: entries are searched by semantic similarity (lazily initializing
// the embedder) or by substring match on title and content as a fallback.
//
// On a single match, the entry is returned directly.
// On multiple matches, the candidates are printed and an error is returned.
// On no matches, an error is returned.
func resolveEntry(ctx context.Context, app *App, arg string) (model.ID, error) {
	// Fast path: valid ULID.
	if id, err := model.ParseID(arg); err == nil {
		return id, nil
	}

	// Search for candidates.
	candidates, err := searchEntries(ctx, app, arg)
	if err != nil {
		return model.ID{}, fmt.Errorf("search entries: %w", err)
	}

	switch len(candidates) {
	case 0:
		return model.ID{}, fmt.Errorf("no entries matching %q", arg)
	case 1:
		if !app.Printer.quiet {
			fmt.Fprintf(app.Printer.w, "Resolved %q → %s\n", arg, formatCandidate(candidates[0]))
		}
		return candidates[0].ID, nil
	default:
		// Ambiguous: print candidates.
		fmt.Fprintf(app.Printer.w, "Multiple entries match %q:\n", arg)
		for i, e := range candidates {
			fmt.Fprintf(app.Printer.w, "  %d. %s\n", i+1, formatCandidate(e))
		}
		fmt.Fprintln(app.Printer.w, "Use a more specific query or provide the full ID.")
		return model.ID{}, fmt.Errorf("ambiguous query %q: %d matches", arg, len(candidates))
	}
}

// searchEntries tries semantic search first (lazily initializing the embedder),
// falling back to substring search if the embedder is unavailable or returns no results.
func searchEntries(ctx context.Context, app *App, q string) ([]model.Entry, error) {
	if results, err := searchSemantic(ctx, app, q); err == nil && len(results) > 0 {
		return results, nil
	}

	// Fallback: substring search.
	return searchByText(ctx, app, q)
}

// searchSemantic performs a vector similarity search using the query engine.
// It lazily initializes the embedder if not already present.
func searchSemantic(ctx context.Context, app *App, q string) ([]model.Entry, error) {
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

	results, err := app.Engine.SearchVector(ctx, query.VectorOptions{
		Text:  q,
		Scope: scope,
		Limit: 5,
	})
	if err != nil {
		return nil, err
	}

	entries := make([]model.Entry, len(results))
	for i, r := range results {
		entries[i] = r.Entry
	}
	return entries, nil
}

// searchByText finds entries whose title or content contains the query substring.
// Used as a fallback when semantic search is unavailable.
func searchByText(ctx context.Context, app *App, q string) ([]model.Entry, error) {
	scope := app.Config.DefaultScope
	scopePrefix := ""
	if scope != model.RootScope {
		scopePrefix = scope
	}

	filter := storage.EntryFilter{
		ScopePrefix: scopePrefix,
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
