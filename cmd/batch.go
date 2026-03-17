package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
)

// batchEntry is the JSONL input format for add --batch.
type batchEntry struct {
	Content    string            `json:"content"`
	Title      string            `json:"title,omitempty"`
	Scope      string            `json:"scope,omitempty"`
	SourceType string            `json:"source_type,omitempty"`
	SourceRef  string            `json:"source_ref,omitempty"`
	Provenance string            `json:"provenance,omitempty"`
	TTL        string            `json:"ttl,omitempty"`
	Labels     []string          `json:"labels,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// runAddBatch implements the "known add --batch" mode.
// It reads JSONL from stdin, embeds all entries in one batch, and writes
// them in a single transaction.
func runAddBatch(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("add --batch", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be added without writing")
	scope := fs.String("scope", "", "default scope for entries without one")

	if err := fs.Parse(args); err != nil {
		return err
	}

	defaultScope := app.Config.DefaultScope
	if *scope != "" {
		defaultScope = app.Config.QualifyScope(*scope)
	}

	// Parse JSONL from stdin.
	var inputs []batchEntry
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var b batchEntry
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			return fmt.Errorf("line %d: invalid JSON: %w", lineNum, err)
		}
		if b.Content == "" {
			return fmt.Errorf("line %d: content is required", lineNum)
		}
		inputs = append(inputs, b)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	if len(inputs) == 0 {
		return fmt.Errorf("no entries to add (expected JSONL on stdin)")
	}

	// Build entries and collect texts for batch embedding.
	entries := make([]model.Entry, 0, len(inputs))
	texts := make([]string, 0, len(inputs))

	for i, b := range inputs {
		sourceType := model.SourceType(b.SourceType)
		if sourceType == "" {
			sourceType = model.SourceManual
		}
		sourceRef := b.SourceRef
		if sourceRef == "" {
			sourceRef = "cli"
		}
		source := model.Source{Type: sourceType, Reference: sourceRef}
		if err := source.Validate(); err != nil {
			return fmt.Errorf("entry %d: invalid source: %w", i+1, err)
		}

		if len(b.Content) > app.Config.MaxContentLength {
			return fmt.Errorf("entry %d: content exceeds maximum length (%d > %d)",
				i+1, len(b.Content), app.Config.MaxContentLength)
		}

		entry := model.NewEntry(b.Content, source)
		entry.Title = b.Title

		entryScope := defaultScope
		if b.Scope != "" {
			entryScope = app.Config.QualifyScope(b.Scope)
		}
		entry.Scope = entryScope

		prov := model.ProvenanceInferred
		if b.Provenance != "" {
			prov = model.ProvenanceLevel(b.Provenance)
		}
		entry.Provenance = model.Provenance{Level: prov}

		// TTL handling.
		if b.TTL != "" {
			if b.TTL == "0" {
				entry.TTL = nil
				entry.ExpiresAt = nil
			} else {
				dur, err := time.ParseDuration(b.TTL)
				if err != nil {
					return fmt.Errorf("entry %d: invalid ttl %q: %w", i+1, b.TTL, err)
				}
				entry.SetTTL(dur)
			}
		} else if d, ok := app.Config.DefaultTTL[entry.Source.Type]; ok {
			entry.SetTTL(d)
		}

		// Metadata.
		if len(b.Meta) > 0 {
			meta := make(model.Metadata, len(b.Meta))
			for k, v := range b.Meta {
				meta[k] = v
			}
			entry.Meta = meta
		}

		// Labels.
		if len(b.Labels) > 0 {
			var labels []string
			for _, l := range b.Labels {
				if l != "" {
					labels = append(labels, l)
				}
			}
			entry.Labels = labels
		}

		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entry %d: invalid: %w", i+1, err)
		}

		entries = append(entries, entry)
		texts = append(texts, b.Content)
	}

	stderr := app.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	if *dryRun {
		for i, entry := range entries {
			fmt.Fprintf(stderr, "[dry-run] %d/%d scope=%s content=%q\n",
				i+1, len(entries), entry.Scope, truncate(entry.Content, 80))
		}
		app.Printer.PrintMessage("Dry run: %d entries would be added.", len(entries))
		return nil
	}

	// Batch embed all texts at once.
	fmt.Fprintf(stderr, "Embedding %d entries...\n", len(entries))
	embeddings, err := app.Embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return fmt.Errorf("batch embed: %w", err)
	}

	if len(embeddings) != len(entries) {
		return fmt.Errorf("batch embed: got %d embeddings for %d entries", len(embeddings), len(entries))
	}

	for i := range entries {
		entries[i] = entries[i].WithEmbedding(embeddings[i], app.Embedder.ModelName())
	}

	// Write all entries in a single transaction.
	fmt.Fprintf(stderr, "Writing %d entries...\n", len(entries))
	var created, updated, failed int
	var warnings []string

	// Best-effort: individual entry failures are logged as warnings, not
	// rolled back. The transaction groups writes for performance, not atomicity.
	err = app.DB.WithTx(ctx, func(txCtx context.Context) error {
		for i := range entries {
			result, err := app.Entries.CreateOrUpdate(txCtx, &entries[i])
			if err != nil {
				failed++
				warnings = append(warnings, fmt.Sprintf("entry %d: %v", i+1, err))
				continue
			}
			if result.Version > 1 {
				updated++
			} else {
				created++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("batch write: %w", err)
	}

	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintf(stderr, "warning: %s\n", w)
		}
	}

	app.Printer.PrintMessage("Batch complete: %d created, %d updated, %d failed.", created, updated, failed)
	return nil
}
