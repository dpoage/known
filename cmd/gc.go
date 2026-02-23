package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"
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
	return nil
}
