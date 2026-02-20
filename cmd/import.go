package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

func runImport(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known import <file>\n\nImport entries from a JSON or JSONL file.")
	}

	filePath := fs.Arg(0)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	content := strings.TrimSpace(string(data))

	var exports []exportEntry

	// Detect format: if content starts with '[', assume JSON array.
	if strings.HasPrefix(content, "[") {
		if err := json.Unmarshal(data, &exports); err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}
	} else {
		// JSONL: one object per line.
		scanner := bufio.NewScanner(strings.NewReader(content))
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

	imported := 0
	edgesCreated := 0

	for _, e := range exports {
		entry := e.Entry
		_, err := app.Entries.CreateOrUpdate(ctx, &entry)
		if err != nil {
			return fmt.Errorf("import entry %s: %w", entry.ID, err)
		}
		imported++

		for _, edge := range e.Edges {
			if err := app.Edges.Create(ctx, &edge); err != nil {
				// Edges may already exist; log but continue.
				fmt.Fprintf(os.Stderr, "warning: edge %s: %v\n", edge.ID, err)
				continue
			}
			edgesCreated++
		}
	}

	app.Printer.PrintMessage("Imported %d entries, %d edges.", imported, edgesCreated)
	return nil
}
