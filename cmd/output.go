package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

// Printer handles formatted output for CLI commands.
type Printer struct {
	w     io.Writer
	json  bool
	quiet bool
}

// NewPrinter creates a Printer that writes to w.
func NewPrinter(w io.Writer, jsonMode, quiet bool) *Printer {
	return &Printer{w: w, json: jsonMode, quiet: quiet}
}

// PrintEntry prints a single entry in human-readable or JSON format.
func (p *Printer) PrintEntry(e model.Entry) {
	if p.json {
		p.printJSON(e)
		return
	}
	fmt.Fprintf(p.w, "ID:         %s\n", e.ID)
	if e.Title != "" {
		fmt.Fprintf(p.w, "Title:      %s\n", e.Title)
	}
	fmt.Fprintf(p.w, "Content:    %s\n", truncate(e.Content, 200))
	fmt.Fprintf(p.w, "Scope:      %s\n", e.Scope)
	fmt.Fprintf(p.w, "Source:     %s (%s)\n", e.Source.Reference, e.Source.Type)
	fmt.Fprintf(p.w, "Confidence: %s\n", e.Confidence.Level)
	fmt.Fprintf(p.w, "Version:    %d\n", e.Version)
	fmt.Fprintf(p.w, "Created:    %s\n", e.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(p.w, "Updated:    %s\n", e.UpdatedAt.Format(time.RFC3339))
	if e.ExpiresAt != nil {
		fmt.Fprintf(p.w, "Expires:    %s\n", e.ExpiresAt.Format(time.RFC3339))
	}
	if e.HasEmbedding() {
		fmt.Fprintf(p.w, "Embedding:  %s (%d dims)\n", e.EmbeddingModel, e.EmbeddingDim)
	}
	fmt.Fprintln(p.w)
}

// PrintEntries prints a list of entries.
func (p *Printer) PrintEntries(entries []model.Entry) {
	if p.json {
		p.printJSON(entries)
		return
	}
	if len(entries) == 0 {
		if !p.quiet {
			fmt.Fprintln(p.w, "No entries found.")
		}
		return
	}
	for _, e := range entries {
		p.PrintEntry(e)
	}
	if !p.quiet {
		fmt.Fprintf(p.w, "(%d entries)\n", len(entries))
	}
}

// PrintResults prints query results with scores and reach information.
func (p *Printer) PrintResults(results []query.Result) {
	if p.json {
		p.printJSON(results)
		return
	}
	if len(results) == 0 {
		if !p.quiet {
			fmt.Fprintln(p.w, "No results found.")
		}
		return
	}
	for i, r := range results {
		fmt.Fprintf(p.w, "--- Result %d ---\n", i+1)
		fmt.Fprintf(p.w, "ID:       %s\n", r.Entry.ID)
		if r.Entry.Title != "" {
			fmt.Fprintf(p.w, "Title:    %s\n", r.Entry.Title)
		}
		fmt.Fprintf(p.w, "Content:  %s\n", truncate(r.Entry.Content, 200))
		fmt.Fprintf(p.w, "Scope:    %s\n", r.Entry.Scope)
		if r.Score > 0 {
			fmt.Fprintf(p.w, "Score:    %.4f\n", r.Score)
		}
		if r.Distance > 0 {
			fmt.Fprintf(p.w, "Distance: %.4f\n", r.Distance)
		}
		fmt.Fprintf(p.w, "Reach:    %s\n", r.Reach)
		if r.Depth > 0 {
			fmt.Fprintf(p.w, "Depth:    %d\n", r.Depth)
		}
		if len(r.EdgePath) > 0 {
			fmt.Fprintf(p.w, "Path:     %s\n", formatEdgePath(r.EdgePath))
		}
		if r.HasConflict {
			ids := make([]string, len(r.Conflicts))
			for j, id := range r.Conflicts {
				ids[j] = id.String()
			}
			fmt.Fprintf(p.w, "Conflicts: %s\n", strings.Join(ids, ", "))
		}
		fmt.Fprintln(p.w)
	}
	if !p.quiet {
		fmt.Fprintf(p.w, "(%d results)\n", len(results))
	}
}

// PrintEdge prints a single edge.
func (p *Printer) PrintEdge(e model.Edge) {
	if p.json {
		p.printJSON(e)
		return
	}
	fmt.Fprintf(p.w, "ID:      %s\n", e.ID)
	fmt.Fprintf(p.w, "From:    %s\n", e.FromID)
	fmt.Fprintf(p.w, "To:      %s\n", e.ToID)
	fmt.Fprintf(p.w, "Type:    %s\n", e.Type)
	if e.Weight != nil {
		fmt.Fprintf(p.w, "Weight:  %.2f\n", *e.Weight)
	}
	fmt.Fprintf(p.w, "Created: %s\n", e.CreatedAt.Format(time.RFC3339))
	fmt.Fprintln(p.w)
}

// PrintEdges prints a list of edges.
func (p *Printer) PrintEdges(edges []model.Edge) {
	if p.json {
		p.printJSON(edges)
		return
	}
	for _, e := range edges {
		p.PrintEdge(e)
	}
}

// PrintMessage prints an informational message (suppressed in quiet mode).
func (p *Printer) PrintMessage(format string, args ...any) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.w, format+"\n", args...)
}

// PrintRecallResults prints query results in a clean, LLM-optimized format.
// Output contains scope, title, confidence, source, entry ID, and content.
// No scores, timestamps, or JSON structure. IDs are always included in curly
// braces so agents can act on results (link, update, delete, show).
func (p *Printer) PrintRecallResults(results []query.Result) {
	if len(results) == 0 {
		fmt.Fprintln(p.w, "No matching knowledge found.")
		return
	}
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(p.w)
		}
		var meta string
		if r.Entry.Title != "" {
			meta = fmt.Sprintf("[%s: %s] (%s, source: %s) {%s}",
				r.Entry.Scope, r.Entry.Title, r.Entry.Confidence.Level, r.Entry.Source.Reference, r.Entry.ID)
		} else {
			meta = fmt.Sprintf("[%s] (%s, source: %s) {%s}",
				r.Entry.Scope, r.Entry.Confidence.Level, r.Entry.Source.Reference, r.Entry.ID)
		}
		if r.HasConflict {
			meta += " (conflicts with existing entries)"
		}
		fmt.Fprintln(p.w, meta)
		fmt.Fprintln(p.w, r.Entry.Content)
	}
}

// PrintConflictPairs prints conflict pairs.
func (p *Printer) PrintConflictPairs(pairs []query.ConflictPair) {
	if p.json {
		p.printJSON(pairs)
		return
	}
	if len(pairs) == 0 {
		if !p.quiet {
			fmt.Fprintln(p.w, "No conflicts found.")
		}
		return
	}
	for i, cp := range pairs {
		fmt.Fprintf(p.w, "--- Conflict %d ---\n", i+1)
		fmt.Fprintf(p.w, "Entry A: %s\n", cp.EntryA.ID)
		fmt.Fprintf(p.w, "  %s\n", truncate(cp.EntryA.Content, 120))
		fmt.Fprintf(p.w, "Entry B: %s\n", cp.EntryB.ID)
		fmt.Fprintf(p.w, "  %s\n", truncate(cp.EntryB.Content, 120))
		fmt.Fprintf(p.w, "Edge:    %s\n", cp.Edge.ID)
		fmt.Fprintln(p.w)
	}
	if !p.quiet {
		fmt.Fprintf(p.w, "(%d conflict pairs)\n", len(pairs))
	}
}

// PrintScopes prints a list of scopes.
func (p *Printer) PrintScopes(scopes []model.Scope) {
	if p.json {
		p.printJSON(scopes)
		return
	}
	if len(scopes) == 0 {
		if !p.quiet {
			fmt.Fprintln(p.w, "No scopes found.")
		}
		return
	}
	for _, s := range scopes {
		fmt.Fprintf(p.w, "%s\n", s.Path)
	}
}

// printJSON marshals v as indented JSON and writes it.
func (p *Printer) printJSON(v any) {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// truncate shortens s to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display.
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// formatEdgePath formats a slice of edges as a readable path string.
func formatEdgePath(edges []model.Edge) string {
	parts := make([]string, len(edges))
	for i, e := range edges {
		parts[i] = fmt.Sprintf("%s -[%s]-> %s", e.FromID, e.Type, e.ToID)
	}
	return strings.Join(parts, " | ")
}
