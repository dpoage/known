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

	if err := app.Scopes.EnsureHierarchy(ctx, path); err != nil {
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

	currentScope := app.Config.DefaultScope
	printTree(app.Printer, scopes, currentScope)
	return nil
}

// scopeNode is a tree node used to build the scope hierarchy for display.
type scopeNode struct {
	name     string // last segment of the path
	path     string // full dot-separated path
	children []*scopeNode
}

// buildScopeTree groups sorted scopes into a forest of scopeNodes.
func buildScopeTree(scopes []model.Scope) []*scopeNode {
	roots := []*scopeNode{}
	index := map[string]*scopeNode{}

	for _, s := range scopes {
		parts := strings.Split(s.Path, ".")
		node := &scopeNode{name: parts[len(parts)-1], path: s.Path}
		index[s.Path] = node

		if len(parts) == 1 {
			roots = append(roots, node)
		} else {
			parentPath := strings.Join(parts[:len(parts)-1], ".")
			if parent, ok := index[parentPath]; ok {
				parent.children = append(parent.children, node)
			} else {
				// Orphan — treat as root.
				roots = append(roots, node)
			}
		}
	}
	return roots
}

// printTree displays scopes as a filesystem-style tree with usage context.
// When currentScope matches a scope path, that line is annotated with a marker.
func printTree(p *Printer, scopes []model.Scope, currentScope string) {
	// Sort by path to ensure parents come before children.
	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].Path < scopes[j].Path
	})

	fmt.Fprintln(p.w, "Scopes defined — use /recall '<topic>' to check for stored knowledge:")

	roots := buildScopeTree(scopes)
	for i, root := range roots {
		isLast := i == len(roots)-1
		printNode(p, root, "", isLast, true, currentScope)
	}

	fmt.Fprintf(p.w, "Example: /recall '<topic>' --scope <scope>\n")
}

// printNode recursively prints a scope node with box-drawing characters.
func printNode(p *Printer, node *scopeNode, prefix string, isLast bool, isRoot bool, currentScope string) {
	connector := "├── "
	if isLast {
		connector = "└── "
	}

	marker := ""
	if currentScope != "" && currentScope != model.RootScope && node.path == currentScope {
		marker = "  <-- you are here"
	}

	if isRoot {
		fmt.Fprintf(p.w, "%s%s\n", node.name, marker)
	} else {
		fmt.Fprintf(p.w, "%s%s%s%s\n", prefix, connector, node.name, marker)
	}

	childPrefix := prefix
	if !isRoot {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	for i, child := range node.children {
		childIsLast := i == len(node.children)-1
		printNode(p, child, childPrefix, childIsLast, false, currentScope)
	}
}
