package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

// OutputMode controls the formatting style.
type OutputMode int

const (
	// OutputHuman produces human-readable aligned text.
	OutputHuman OutputMode = iota
	// OutputJSON encodes values as JSON.
	OutputJSON
	// OutputQuiet prints only IDs.
	OutputQuiet
)

// Printer formats and writes CLI output.
type Printer struct {
	w    io.Writer
	mode OutputMode
}

// NewPrinter creates a Printer writing to w in the given mode.
func NewPrinter(w io.Writer, mode OutputMode) *Printer {
	return &Printer{w: w, mode: mode}
}

// PrintEntry outputs a single entry with full details.
func (p *Printer) PrintEntry(e model.Entry) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(e)
	case OutputQuiet:
		fmt.Fprintln(p.w, e.ID.String())
	default:
		p.printEntryHuman(e)
	}
}

// PrintEntries outputs a list of entries.
func (p *Printer) PrintEntries(entries []model.Entry) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(entries)
	case OutputQuiet:
		for _, e := range entries {
			fmt.Fprintln(p.w, e.ID.String())
		}
	default:
		if len(entries) == 0 {
			fmt.Fprintln(p.w, "No entries found.")
			return
		}
		for i, e := range entries {
			if i > 0 {
				fmt.Fprintln(p.w, "---")
			}
			p.printEntryHuman(e)
		}
	}
}

// PrintResults outputs query results with scores.
func (p *Printer) PrintResults(results []query.Result) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(results)
	case OutputQuiet:
		for _, r := range results {
			fmt.Fprintln(p.w, r.Entry.ID.String())
		}
	default:
		if len(results) == 0 {
			fmt.Fprintln(p.w, "No results found.")
			return
		}
		tw := tabwriter.NewWriter(p.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "ID\tSCORE\tSCOPE\tREACH\tCONTENT\n")
		for _, r := range results {
			content := truncate(r.Entry.Content, 60)
			fmt.Fprintf(tw, "%s\t%.4f\t%s\t%s\t%s\n",
				r.Entry.ID.String(), r.Score, r.Entry.Scope,
				string(r.Reach), content)
		}
		tw.Flush()
	}
}

// PrintEdge outputs a single edge.
func (p *Printer) PrintEdge(e model.Edge) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(e)
	case OutputQuiet:
		fmt.Fprintln(p.w, e.ID.String())
	default:
		p.printEdgeHuman(e)
	}
}

// PrintEdges outputs a list of edges.
func (p *Printer) PrintEdges(edges []model.Edge) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(edges)
	case OutputQuiet:
		for _, e := range edges {
			fmt.Fprintln(p.w, e.ID.String())
		}
	default:
		if len(edges) == 0 {
			return
		}
		for _, e := range edges {
			p.printEdgeHuman(e)
		}
	}
}

// PrintMessage outputs a confirmation or informational message.
// In quiet mode, nothing is printed.
func (p *Printer) PrintMessage(msg string) {
	switch p.mode {
	case OutputJSON:
		p.encodeJSON(map[string]string{"message": msg})
	case OutputQuiet:
		// silence
	default:
		fmt.Fprintln(p.w, msg)
	}
}

// printEntryHuman renders an entry in human-readable format.
func (p *Printer) printEntryHuman(e model.Entry) {
	fmt.Fprintf(p.w, "ID:          %s\n", e.ID.String())
	fmt.Fprintf(p.w, "Scope:       %s\n", e.Scope)
	fmt.Fprintf(p.w, "Content:     %s\n", e.Content)
	fmt.Fprintf(p.w, "Source:      %s (%s)\n", e.Source.Reference, e.Source.Type)
	fmt.Fprintf(p.w, "Confidence:  %s\n", e.Confidence.Level)
	fmt.Fprintf(p.w, "Version:     %d\n", e.Version)
	fmt.Fprintf(p.w, "Created:     %s\n", e.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(p.w, "Updated:     %s\n", e.UpdatedAt.Format(time.RFC3339))
	if e.HasEmbedding() {
		fmt.Fprintf(p.w, "Embedding:   %s (%d dims)\n", e.EmbeddingModel, e.EmbeddingDim)
	}
	if e.TTL != nil {
		fmt.Fprintf(p.w, "TTL:         %s\n", e.TTL.Duration.String())
	}
	if e.ExpiresAt != nil {
		fmt.Fprintf(p.w, "Expires:     %s\n", e.ExpiresAt.Format(time.RFC3339))
	}
	if len(e.Meta) > 0 {
		fmt.Fprintf(p.w, "Meta:        %s\n", formatMeta(e.Meta))
	}
	if e.HasConflicts() {
		ids := make([]string, len(e.ConflictsWith))
		for i, id := range e.ConflictsWith {
			ids[i] = id.String()
		}
		fmt.Fprintf(p.w, "Conflicts:   %s\n", strings.Join(ids, ", "))
	}
}

// printEdgeHuman renders an edge in human-readable format.
func (p *Printer) printEdgeHuman(e model.Edge) {
	fmt.Fprintf(p.w, "  %s -[%s]-> %s\n", e.FromID.String(), e.Type, e.ToID.String())
}

// encodeJSON writes v as indented JSON to the printer's writer.
func (p *Printer) encodeJSON(v any) {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// formatMeta formats metadata as a compact key=value string.
func formatMeta(m model.Metadata) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ", ")
}

// truncate shortens s to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
