package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// primeDoc is the agent-facing guidance printed by "known prime". It is
// generated from plugin/skills/known/SKILL.md — run "go generate ./cmd/scaffold"
// to regenerate. Embedding it keeps the guidance in lockstep with the
// installed binary: no plugin, scaffold, or network required.
//
//go:embed prime.md
var primeDoc string

// runPrime implements the "known prime" subcommand: print the embedded
// guidance, then append a best-effort live status footer. The guidance is
// the deliverable — a missing or unreachable database only downgrades the
// footer to a stderr note, never a failure. Dispatched before initApp in
// Run for exactly that reason.
func runPrime(ctx context.Context, gf globalFlags) int {
	fmt.Print(primeDoc)

	app, err := initApp(ctx, gf, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: graph status unavailable: %v\n", err)
		return 0
	}
	defer app.Close()

	printPrimeStatus(ctx, app)
	return 0
}

// printPrimeStatus appends a "## Status" section describing the live graph:
// the scope auto-derived from cwd with its entry count, and graph totals.
// Any storage error is reported to app.Stderr and the section is dropped —
// prime output must stay valid guidance regardless.
func printPrimeStatus(ctx context.Context, app *App) {
	scope := app.Config.DefaultScope
	if scope == "" {
		scope = model.RootScope
	}

	scoped, err := app.Entries.List(ctx, storage.EntryFilter{ScopePrefix: scope})
	if err != nil {
		fmt.Fprintf(app.Stderr, "note: graph status unavailable: %v\n", err)
		return
	}
	total, err := app.Entries.List(ctx, storage.EntryFilter{})
	if err != nil {
		fmt.Fprintf(app.Stderr, "note: graph status unavailable: %v\n", err)
		return
	}
	scopes, err := app.Scopes.List(ctx)
	if err != nil {
		fmt.Fprintf(app.Stderr, "note: graph status unavailable: %v\n", err)
		return
	}

	w := app.Printer.w
	fmt.Fprintf(w, "\n## Status\n\n")
	fmt.Fprintf(w, "Scope  %s — %s (auto-derived from cwd)\n", scope, countEntries(len(scoped)))
	fmt.Fprintf(w, "Graph  %s across %s\n", countEntries(len(total)), countScopes(len(scopes)))
}

// countEntries renders an entry count with correct singular/plural grammar.
func countEntries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// countScopes renders a scope count with correct singular/plural grammar.
func countScopes(n int) string {
	if n == 1 {
		return "1 scope"
	}
	return fmt.Sprintf("%d scopes", n)
}
