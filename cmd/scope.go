package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/postgres"
)

func scopeUsage() string {
	return `Usage: known scope <command> [args]

Manage scopes.

Commands:
  list     List all scopes
  create   Create a scope and its parent hierarchy
  delete   Delete a scope (and optionally its entries and descendants)
  tree     Display scopes as an indented tree

Run 'known scope <command> --help' for details on a specific command.`
}

func runScope(ctx context.Context, app *App, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", scopeUsage())
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "--help", "-h":
		fmt.Fprintln(app.Printer.w, scopeUsage())
		return flag.ErrHelp
	case "list":
		return runScopeList(ctx, app, subArgs)
	case "create":
		return runScopeCreate(ctx, app, subArgs)
	case "delete":
		return runScopeDelete(ctx, app, subArgs)
	case "tree":
		return runScopeTree(ctx, app, subArgs)
	default:
		return fmt.Errorf("unknown scope subcommand: %s (expected list, create, delete, or tree)", subcmd)
	}
}

func runScopeList(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("scope list", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	scopes, err := app.Scopes.List(ctx)
	if err != nil {
		return fmt.Errorf("list scopes: %w", err)
	}

	app.Printer.PrintScopes(scopes)
	return nil
}

func runScopeCreate(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("scope create", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known scope create <path>\n\nCreate a scope and its parent hierarchy.")
	}

	path := fs.Arg(0)

	// Validate the scope path.
	if _, err := model.ParseScopePath(path); err != nil {
		return fmt.Errorf("invalid scope path: %w", err)
	}

	// EnsureHierarchy is on the concrete ScopeStore, not the interface.
	scopeStore, ok := app.Scopes.(*postgres.ScopeStore)
	if !ok {
		// Fallback: upsert the scope directly.
		scope := model.NewScope(path)
		if err := app.Scopes.Upsert(ctx, &scope); err != nil {
			return fmt.Errorf("create scope: %w", err)
		}
		app.Printer.PrintMessage("Scope %s created.", path)
		return nil
	}

	if err := scopeStore.EnsureHierarchy(ctx, path); err != nil {
		return fmt.Errorf("ensure hierarchy: %w", err)
	}

	app.Printer.PrintMessage("Scope hierarchy created for %s.", path)
	return nil
}

func runScopeDelete(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("scope delete", flag.ContinueOnError)
	force := fs.Bool("force", false, "skip confirmation prompt")
	recursive := fs.Bool("recursive", false, "delete all entries and descendant scopes")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known scope delete <path> [--force] [--recursive]\n\nDelete a scope.")
	}

	path := fs.Arg(0)

	// Validate scope exists.
	if _, err := app.Scopes.Get(ctx, path); err != nil {
		if err == storage.ErrNotFound {
			return fmt.Errorf("scope %q not found", path)
		}
		return fmt.Errorf("get scope: %w", err)
	}

	// Check for entries and descendant scopes.
	entries, err := app.Entries.List(ctx, storage.EntryFilter{ScopePrefix: path, Limit: 1})
	if err != nil {
		return fmt.Errorf("check entries: %w", err)
	}
	descendants, err := app.Scopes.ListDescendants(ctx, path)
	if err != nil {
		return fmt.Errorf("list descendants: %w", err)
	}

	hasContent := len(entries) > 0 || len(descendants) > 0

	if hasContent && !*recursive {
		// Count entries for a better message (Limit: 0 means unlimited).
		allEntries, err := app.Entries.List(ctx, storage.EntryFilter{ScopePrefix: path})
		if err != nil {
			return fmt.Errorf("count entries: %w", err)
		}
		return fmt.Errorf("scope %q has %d entries and %d descendant scopes; use --recursive to delete everything", path, len(allEntries), len(descendants))
	}

	if *recursive && hasContent {
		if !*force {
			if app.Config.Quiet {
				return fmt.Errorf("--force is required in quiet mode")
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("stdin is not a terminal; use --force to confirm deletion non-interactively")
			}
			// Count entries for confirmation (Limit: 0 means unlimited).
			allEntries, err := app.Entries.List(ctx, storage.EntryFilter{ScopePrefix: path})
			if err != nil {
				return fmt.Errorf("count entries: %w", err)
			}
			fmt.Fprintf(os.Stderr, "This will delete %d entries and %d descendant scopes under %q. Continue? [y/N] ",
				len(allEntries), len(descendants), path)
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				app.Printer.PrintMessage("Cancelled.")
				return nil
			}
		}

		// Delete inside a transaction: entries, descendant scopes (deepest first), then target scope.
		if err := app.DB.WithTx(ctx, func(txCtx context.Context) error {
			allEntries, err := app.Entries.List(txCtx, storage.EntryFilter{ScopePrefix: path})
			if err != nil {
				return fmt.Errorf("list entries: %w", err)
			}
			for _, e := range allEntries {
				if err := app.Entries.Delete(txCtx, e.ID); err != nil {
					return fmt.Errorf("delete entry %s: %w", e.ID, err)
				}
			}
			// Re-fetch descendants inside the transaction for consistency.
			txDescendants, err := app.Scopes.ListDescendants(txCtx, path)
			if err != nil {
				return fmt.Errorf("list descendants: %w", err)
			}
			// Delete descendants deepest-first.
			for i := len(txDescendants) - 1; i >= 0; i-- {
				if err := app.Scopes.Delete(txCtx, txDescendants[i].Path); err != nil {
					return fmt.Errorf("delete scope %s: %w", txDescendants[i].Path, err)
				}
			}
			return app.Scopes.Delete(txCtx, path)
		}); err != nil {
			return fmt.Errorf("delete scope: %w", err)
		}
		app.Printer.PrintMessage("Deleted scope %s and all its contents.", path)
		return nil
	}

	// No content — just delete the scope.
	if !*force {
		if app.Config.Quiet {
			return fmt.Errorf("--force is required in quiet mode")
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("stdin is not a terminal; use --force to confirm deletion non-interactively")
		}
		fmt.Fprintf(os.Stderr, "Delete scope %q? [y/N] ", path)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			app.Printer.PrintMessage("Cancelled.")
			return nil
		}
	}

	if err := app.Scopes.Delete(ctx, path); err != nil {
		return fmt.Errorf("delete scope: %w", err)
	}
	app.Printer.PrintMessage("Deleted scope %s.", path)
	return nil
}

func runScopeTree(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("scope tree", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	scopes, err := app.Scopes.List(ctx)
	if err != nil {
		return fmt.Errorf("list scopes: %w", err)
	}

	if app.Printer.json {
		app.Printer.printJSON(scopes)
		return nil
	}

	if len(scopes) == 0 {
		app.Printer.PrintMessage("No scopes found.")
		return nil
	}

	printTree(app.Printer, scopes)
	return nil
}

// printTree displays scopes as an indented tree with usage context.
func printTree(p *Printer, scopes []model.Scope) {
	// Sort by path to ensure parents come before children.
	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].Path < scopes[j].Path
	})

	fmt.Fprintln(p.w, "Knowledge available — use /recall before exploring:")

	for _, s := range scopes {
		depth := strings.Count(s.Path, ".")
		indent := strings.Repeat("  ", depth)

		// Extract the last segment for display.
		parts := strings.Split(s.Path, ".")
		name := parts[len(parts)-1]
		if depth == 0 {
			name = "/" + name
		}

		fmt.Fprintf(p.w, "%s%s\n", indent, name)
	}

	fmt.Fprintf(p.w, "Example: known recall '<topic>' --scope <scope>\n")
}
