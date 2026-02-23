package cmd

import (
	"context"
	flag "github.com/spf13/pflag"
	"fmt"

	"github.com/dpoage/known/model"
)

func runUnlink(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("unlink", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: known unlink <edge-id>\n\nDelete an edge by ID.")
	}

	edgeID, err := model.ParseID(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid edge ID: %w", err)
	}

	if err := app.Edges.Delete(ctx, edgeID); err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}

	app.Printer.PrintMessage("Edge %s deleted.", edgeID)
	return nil
}
