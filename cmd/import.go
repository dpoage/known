package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	flag "github.com/spf13/pflag"
)

func runImport(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	reEmbed := fs.Bool("re-embed", false, "recompute embeddings with the current model")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known import <file> [--re-embed]\n\nImport entries from a JSON or JSONL file.")
	}

	filePath := fs.Arg(0)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	data = bytes.TrimSpace(data)

	var exports []exportEntry

	// Detect format: if content starts with '[', assume JSON array.
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &exports); err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}
	} else {
		// JSONL: one object per line.
		scanner := bufio.NewScanner(bytes.NewReader(data))
		// Increase buffer for large entries.
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var e exportEntry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return fmt.Errorf("parse JSONL line: %w", err)
			}
			exports = append(exports, e)
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read JSONL: %w", err)
		}
	}

	stderr := app.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Re-embed: batch-embed all entry contents with the current model.
	if *reEmbed {
		if app.Embedder == nil {
			return fmt.Errorf("--re-embed requires an embedder (none configured)")
		}

		texts := make([]string, len(exports))
		for i, e := range exports {
			texts[i] = e.Entry.Content
		}

		fmt.Fprintf(stderr, "Re-embedding %d entries...\n", len(texts))
		embeddings, err := app.Embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return fmt.Errorf("batch embed: %w", err)
		}

		if len(embeddings) != len(exports) {
			return fmt.Errorf("batch embed: got %d embeddings for %d entries", len(embeddings), len(exports))
		}

		modelName := app.Embedder.ModelName()
		for i := range exports {
			exports[i].Entry = exports[i].Entry.WithEmbedding(embeddings[i], modelName)
		}
	}

	imported := 0
	edgesCreated := 0

	fmt.Fprintf(stderr, "Writing %d entries...\n", len(exports))
	err = app.DB.WithTx(ctx, func(txCtx context.Context) error {
		for _, e := range exports {
			entry := e.Entry
			_, err := app.Entries.CreateOrUpdate(txCtx, &entry)
			if err != nil {
				return fmt.Errorf("import entry %s: %w", entry.ID, err)
			}
			imported++

			for _, edge := range e.Edges {
				if err := app.Edges.Create(txCtx, &edge); err != nil {
					// Edges may already exist; log but continue.
					fmt.Fprintf(stderr, "warning: edge %s: %v\n", edge.ID, err)
					continue
				}
				edgesCreated++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}

	app.Printer.PrintMessage("Imported %d entries, %d edges.", imported, edgesCreated)
	return nil
}
