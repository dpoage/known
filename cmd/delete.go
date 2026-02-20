package cmd

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dpoage/known/model"
)

// runDelete implements the "known delete" subcommand.
//
// Usage: known delete <id> [--force]
//
// Without --force, shows the entry and prompts for confirmation.
// In quiet mode, --force is required.
func runDelete(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	force := fs.Bool("force", false, "skip confirmation prompt")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return fmt.Errorf("entry ID is required\nUsage: known delete <id> [--force]")
	}

	id, err := model.ParseID(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid entry ID: %w", err)
	}

	// Fetch the entry to confirm it exists (and to display it).
	entry, err := app.Entries.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get entry: %w", err)
	}

	// Require confirmation unless --force is set.
	if !*force {
		if app.Config.Quiet {
			return fmt.Errorf("--force is required in quiet mode")
		}

		// Show what will be deleted.
		app.Printer.PrintEntry(*entry)
		fmt.Fprintf(os.Stderr, "\nDelete this entry? [y/N] ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			app.Printer.PrintMessage("Cancelled.")
			return nil
		}
	}

	if err := app.Entries.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}

	app.Printer.PrintMessage("Deleted entry %s", id.String())
	return nil
}
