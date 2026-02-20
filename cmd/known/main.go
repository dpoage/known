// Command known is the CLI entry point for the knowledge graph.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/dpoage/known/cmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	os.Exit(cmd.Run(ctx, os.Args[1:]))
}
