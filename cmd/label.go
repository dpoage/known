package cmd

import (
	"context"
	"fmt"

	flag "github.com/spf13/pflag"
)

func labelUsage() string {
	return `Usage: known label <command>

Manage labels.

Commands:
  list     List all labels

Run 'known label <command> --help' for details on a specific command.`
}

func runLabel(ctx context.Context, app *App, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", labelUsage())
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "--help", "-h":
		fmt.Fprintln(app.Printer.w, labelUsage())
		return flag.ErrHelp
	case "list":
		return runLabelList(ctx, app, subArgs)
	default:
		return fmt.Errorf("unknown label subcommand: %s (expected list)", subcmd)
	}
}

func runLabelList(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("label list", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	labels, err := app.DB.Labels().ListLabels(ctx)
	if err != nil {
		return fmt.Errorf("list labels: %w", err)
	}

	if app.Printer.json {
		app.Printer.printJSON(labels)
		return nil
	}

	if len(labels) == 0 {
		app.Printer.PrintMessage("No labels found.")
		return nil
	}

	for _, label := range labels {
		fmt.Fprintln(app.Printer.w, label)
	}
	return nil
}
