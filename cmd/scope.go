package cmd

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage/postgres"
)

func runScope(ctx context.Context, app *App, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: known scope <list|create|tree> [args]\n\nManage scopes.")
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "list":
		return runScopeList(ctx, app, subArgs)
	case "create":
		return runScopeCreate(ctx, app, subArgs)
	case "tree":
		return runScopeTree(ctx, app, subArgs)
	default:
		return fmt.Errorf("unknown scope subcommand: %s (expected list, create, or tree)", subcmd)
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

// printTree displays scopes as an indented tree.
func printTree(p *Printer, scopes []model.Scope) {
	// Sort by path to ensure parents come before children.
	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].Path < scopes[j].Path
	})

	for _, s := range scopes {
		depth := strings.Count(s.Path, ".")
		indent := strings.Repeat("  ", depth)

		// Extract the last segment for display.
		parts := strings.Split(s.Path, ".")
		name := parts[len(parts)-1]

		fmt.Fprintf(p.w, "%s%s\n", indent, name)
	}
}
