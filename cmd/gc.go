package cmd

import (
	"context"
	"fmt"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/query"
)

func runGC(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	count, err := app.Entries.DeleteExpired(ctx)
	if err != nil {
		return fmt.Errorf("delete expired: %w", err)
	}

	app.Printer.PrintMessage("Deleted %d expired entries.", count)

	scopeCount, err := app.Scopes.DeleteEmpty(ctx)
	if err != nil {
		return fmt.Errorf("prune empty scopes: %w", err)
	}

	if scopeCount > 0 {
		app.Printer.PrintMessage("Pruned %d empty scopes.", scopeCount)
	}

	// Reinforce edge weights based on session usage signals.
	cfg := query.DefaultReinforceConfig()
	result, err := app.Engine.Reinforce(ctx, app.Sessions, cfg)
	if err != nil {
		app.Printer.PrintMessage("Warning: edge reinforcement: %v", err)
	} else if result.SessionsProcessed > 0 {
		app.Printer.PrintMessage("Reinforced %d edges from %d sessions.",
			result.EdgesBoosted, result.SessionsProcessed)
	}

	return nil
}
