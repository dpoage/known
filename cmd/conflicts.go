package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"

	"github.com/dpoage/known/model"
)

func runConflicts(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("conflicts", flag.ContinueOnError)
	scope := fs.String("scope", "", "detect all conflicts within this scope")

	if err := fs.Parse(args); err != nil {
		return err
	}

	*scope = app.Config.QualifyScope(*scope)

	// If a positional argument is given, treat it as an entry ID.
	if fs.NArg() > 0 {
		entryID, err := model.ParseID(fs.Arg(0))
		if err != nil {
			return fmt.Errorf("invalid ID: %w", err)
		}

		results, err := app.Engine.FindConflicts(ctx, entryID)
		if err != nil {
			return fmt.Errorf("find conflicts: %w", err)
		}

		app.Printer.PrintResults(results)
		return nil
	}

	// No positional arg: require --scope to detect all conflicts.
	if *scope == "" {
		return fmt.Errorf("usage: known conflicts <id> or known conflicts --scope=<scope>\n\nDetect conflicting entries.")
	}

	pairs, err := app.Engine.DetectAllConflicts(ctx, *scope)
	if err != nil {
		return fmt.Errorf("detect conflicts: %w", err)
	}

	app.Printer.PrintConflictPairs(pairs)
	return nil
}
