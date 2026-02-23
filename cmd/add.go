package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"
	"strings"
	"time"

	"github.com/dpoage/known/model"
)

// runAdd implements the "known add" subcommand.
//
// Usage: known add 'content' [flags]
//
//	--scope          Scope path (default: "root")
//	--source-type    Source type: file, url, conversation, manual (default: "manual")
//	--source-ref     Source reference (default: "cli")
//	--confidence     Confidence level: verified, inferred, uncertain (default: "inferred")
//	--ttl            Time-to-live duration (e.g., "24h", "168h")
//	--meta           Metadata key=value pairs (repeatable)
//	--link           Create edge: type:target-id (repeatable, e.g. --link elaborates:01KJ...)
func runAdd(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	title := fs.String("title", "", "short label for the entry (2-5 words)")
	scope := fs.String("scope", "", "scope path (default: auto from cwd)")
	sourceType := fs.String("source-type", "manual", "source type (file, url, conversation, manual)")
	sourceRef := fs.String("source-ref", "cli", "source reference")
	confidence := fs.String("confidence", "inferred", "confidence level (verified, inferred, uncertain)")
	ttl := fs.String("ttl", "", "time-to-live (e.g., 24h, 168h)")
	var metaFlags multiFlag
	fs.Var(&metaFlags, "meta", "metadata key=value (repeatable)")
	var linkFlags multiFlag
	fs.Var(&linkFlags, "link", "create edge: type:target-id (repeatable, e.g. --link elaborates:01KJ...)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	} else {
		*scope = app.Config.QualifyScope(*scope)
	}

	if fs.NArg() == 0 {
		return fmt.Errorf("content argument is required\nUsage: known add 'content' [flags]")
	}
	content := fs.Arg(0)

	// Content length check.
	if len(content) > app.Config.MaxContentLength {
		return fmt.Errorf("content exceeds maximum length (%d > %d); break into smaller entries",
			len(content), app.Config.MaxContentLength)
	}

	// Build the entry.
	source := model.Source{
		Type:      model.SourceType(*sourceType),
		Reference: *sourceRef,
	}
	if err := source.Validate(); err != nil {
		return fmt.Errorf("invalid source: %w", err)
	}

	entry := model.NewEntry(content, source)
	entry.Title = *title
	entry.Scope = *scope
	entry.Confidence = model.Confidence{
		Level: model.ConfidenceLevel(*confidence),
	}

	// Parse TTL if provided, otherwise apply default by source type.
	// --ttl 0 means "permanent" (no TTL, no ExpiresAt), same as update.go.
	if *ttl != "" {
		if *ttl == "0" {
			// Explicit zero: permanent entry, skip default TTL.
			entry.TTL = nil
			entry.ExpiresAt = nil
		} else {
			dur, err := time.ParseDuration(*ttl)
			if err != nil {
				return fmt.Errorf("invalid ttl %q: %w", *ttl, err)
			}
			entry.SetTTL(dur)
		}
	} else if d, ok := app.Config.DefaultTTL[entry.Source.Type]; ok {
		entry.SetTTL(d)
	}

	// Parse metadata.
	if len(metaFlags) > 0 {
		meta := make(model.Metadata, len(metaFlags))
		for _, kv := range metaFlags {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("invalid meta format %q: expected key=value", kv)
			}
			meta[k] = v
		}
		entry.Meta = meta
	}

	// Generate embedding.
	embedding, err := app.Embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	entry = entry.WithEmbedding(embedding, app.Embedder.ModelName())

	// Validate before persisting.
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("invalid entry: %w", err)
	}

	// Persist with upsert semantics (dedup by content hash + scope).
	result, err := app.Entries.CreateOrUpdate(ctx, &entry)
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	if result.Version > 1 {
		app.Printer.PrintMessage("Updated existing entry %s (v%d)", result.ID, result.Version)
	}

	app.Printer.PrintEntry(*result)

	// Create edges if --link flags were provided.
	for _, linkSpec := range linkFlags {
		edgeType, targetIDStr, ok := strings.Cut(linkSpec, ":")
		if !ok {
			return fmt.Errorf("invalid --link format %q: expected type:target-id (e.g. elaborates:01KJ...)", linkSpec)
		}

		et := model.EdgeType(edgeType)
		if err := et.Validate(); err != nil {
			return fmt.Errorf("--link %q: invalid edge type: %w", linkSpec, err)
		}

		targetID, err := model.ParseID(targetIDStr)
		if err != nil {
			return fmt.Errorf("--link %q: invalid target ID: %w", linkSpec, err)
		}

		if _, err := app.Entries.Get(ctx, targetID); err != nil {
			return fmt.Errorf("--link %q: target entry %s: %w", linkSpec, targetID, err)
		}

		edge := model.NewEdge(result.ID, targetID, et)
		if err := app.Edges.Create(ctx, &edge); err != nil {
			return fmt.Errorf("--link %q: create edge: %w", linkSpec, err)
		}
		app.Printer.PrintMessage("Linked %s -[%s]-> %s", result.ID, et, targetID)
	}

	return nil
}

// multiFlag accumulates repeated flag values into a slice.
type multiFlag []string

func (f *multiFlag) String() string { return strings.Join(*f, ", ") }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
func (f *multiFlag) Type() string { return "string" }
