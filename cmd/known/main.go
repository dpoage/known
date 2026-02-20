// Command known is a CLI for managing a knowledge graph for LLMs.
//
// Usage:
//
//	known [flags] <command> [command-flags] [args]
//
// Commands:
//
//	add       Add a new knowledge entry
//	update    Update an existing entry
//	delete    Delete an entry
//	show      Show entry details with relationships
//
// Global flags:
//
//	--dsn     PostgreSQL connection string (env: KNOWN_DSN)
//	--json    Output as JSON
//	--quiet   Minimal output (IDs only)
package main

import (
	"fmt"
	"os"

	"github.com/dpoage/known/cmd"
)

func main() {
	if err := cmd.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
