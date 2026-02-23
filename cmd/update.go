package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// runUpdate implements the "known update" subcommand.
//
// Usage: known update <id> [flags]
//
//	--title        New title (short label; use --title="" to clear)
//	--content      New content text
//	--provenance   New provenance level (verified, inferred, uncertain)
//	--scope        New scope path
//	--source-type  New source type (file, url, conversation, manual)
//	--source-ref   New source reference
//	--ttl          New time-to-live (e.g., 24h, 168h; "0" to clear)
//	--meta         Metadata key=value (repeatable, merges with existing; key= to delete)
func runUpdate(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	title := fs.String("title", "", "new title (short label; use --title='' to clear)")
	content := fs.String("content", "", "new content text")
	provenance := fs.String("provenance", "", "new provenance level (verified, inferred, uncertain)")
	observedBy := fs.String("observed-by", "", "who observed this fact (e.g., user, agent)")
	scope := fs.String("scope", "", "new scope path")
	sourceType := fs.String("source-type", "", "new source type (file, url, conversation, manual)")
	sourceRef := fs.String("source-ref", "", "new source reference")
	ttl := fs.String("ttl", "", "new TTL (e.g., 24h, 168h; \"0\" to clear)")
	var metaFlags multiFlag
	fs.Var(&metaFlags, "meta", "metadata key=value (repeatable, merges; key= to delete)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return fmt.Errorf("entry ID is required\nUsage: known update <id> [flags]")
	}

	id, err := model.ParseID(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid entry ID: %w", err)
	}

	// Fetch current entry.
	entry, err := app.Entries.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get entry: %w", err)
	}

	if fs.Changed("title") {
		entry.Title = *title
	}

	// Track whether content changed so we know to re-embed.
	contentChanged := false

	if *content != "" {
		if len(*content) > app.Config.MaxContentLength {
			return fmt.Errorf("content exceeds maximum length (%d > %d); break into smaller entries",
				len(*content), app.Config.MaxContentLength)
		}
		entry.Content = *content
		entry.ContentHash = model.ComputeContentHash(*content)
		contentChanged = true
	}

	if *provenance != "" {
		entry.Provenance.Level = model.ProvenanceLevel(*provenance)
	}
	if *observedBy != "" {
		entry.Freshness.ObservedBy = *observedBy
	}

	if *scope != "" {
		entry.Scope = app.Config.QualifyScope(*scope)
	}

	if *sourceType != "" {
		entry.Source.Type = model.SourceType(*sourceType)
	}

	if *sourceRef != "" {
		entry.Source.Reference = *sourceRef
	}

	if fs.Changed("ttl") {
		if *ttl == "0" || *ttl == "" {
			entry.TTL = nil
			entry.ExpiresAt = nil
		} else {
			dur, err := time.ParseDuration(*ttl)
			if err != nil {
				return fmt.Errorf("invalid ttl %q: %w", *ttl, err)
			}
			entry.SetTTL(dur)
		}
	}

	// Merge metadata: add/update keys, delete keys with empty values.
	if len(metaFlags) > 0 {
		if entry.Meta == nil {
			entry.Meta = make(model.Metadata)
		}
		for _, kv := range metaFlags {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("invalid meta format %q: expected key=value", kv)
			}
			if v == "" {
				delete(entry.Meta, k)
			} else {
				entry.Meta[k] = v
			}
		}
		// Clean up empty map so it serializes as null, not {}.
		if len(entry.Meta) == 0 {
			entry.Meta = nil
		}
	}

	// Re-embed if content changed.
	if contentChanged {
		embedding, err := app.Embedder.Embed(ctx, entry.Content)
		if err != nil {
			return fmt.Errorf("generate embedding: %w", err)
		}
		entry.Embedding = embedding
		entry.EmbeddingDim = len(embedding)
		entry.EmbeddingModel = app.Embedder.ModelName()
	}

	entry.Touch()

	// Validate before persisting.
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("invalid entry: %w", err)
	}

	// Persist with optimistic concurrency control.
	if err := app.Entries.Update(ctx, entry); err != nil {
		if storage.IsConcurrentModification(err) {
			return fmt.Errorf("entry was modified by another process since you read it; re-fetch and try again: %w", err)
		}
		return fmt.Errorf("update entry: %w", err)
	}

	app.Printer.PrintEntry(*entry)
	return nil
}
