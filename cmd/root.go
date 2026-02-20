// Package cmd implements the CLI commands for the known knowledge graph.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/postgres"
)

// App holds the shared dependencies for all CLI commands.
type App struct {
	DB       *postgres.DB
	Entries  storage.EntryRepo
	Edges    storage.EdgeRepo
	Scopes   storage.ScopeRepo
	Embedder embed.Embedder
	Engine   *query.Engine
	Printer  *Printer
	Config   *AppConfig
}

// globalFlags holds the flags parsed from the root command.
type globalFlags struct {
	dsn   string
	json  bool
	quiet bool
}

// parseGlobalFlags extracts global flags from args and returns remaining args.
// Global flags must appear before the subcommand. The standard flag package
// stops parsing at the first non-flag argument, so subcommand names and their
// flags are returned as the remaining args.
func parseGlobalFlags(args []string) (globalFlags, []string) {
	var gf globalFlags

	fs := flag.NewFlagSet("known", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // suppress default error output
	fs.StringVar(&gf.dsn, "dsn", "", "PostgreSQL connection string (env: KNOWN_DSN)")
	fs.BoolVar(&gf.json, "json", false, "output as JSON")
	fs.BoolVar(&gf.quiet, "quiet", false, "suppress non-essential output")

	// Parse stops at the first non-flag argument (the subcommand name).
	// Errors are ignored so that subcommand-specific flags like --help
	// do not cause a failure here.
	_ = fs.Parse(args)

	return gf, fs.Args()
}

// initApp creates and initializes the App from the given flags and config.
// The embedder is only initialized when needsEmbedder is true (for search).
// The query engine is always created since graph traversal commands use it
// without requiring an embedder.
func initApp(ctx context.Context, gf globalFlags, needsEmbedder bool) (*App, error) {
	cfg, err := loadAppConfig(gf)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	db, err := postgres.New(ctx, postgres.Config{DSN: cfg.DSN})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Scopes:  db.Scopes(),
		Printer: NewPrinter(os.Stdout, cfg.JSON, cfg.Quiet),
		Config:  cfg,
	}

	if needsEmbedder {
		embedCfg := embed.LoadConfig()
		embedder, err := embed.NewEmbedder(embedCfg)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("create embedder: %w", err)
		}
		app.Embedder = embedder
		app.Engine = query.New(app.Entries, app.Edges, embedder)
	} else {
		// Create engine without embedder for graph-only commands.
		// SearchVector/SearchHybrid will panic if called without an embedder,
		// but Traverse, FindPath, FindConflicts, and DetectAllConflicts are safe.
		app.Engine = query.New(app.Entries, app.Edges, nil)
	}

	return app, nil
}

// Close releases all resources held by the App.
func (a *App) Close() {
	if a.DB != nil {
		a.DB.Close()
	}
}

// usage prints the top-level help message.
func usage() {
	fmt.Fprintf(os.Stderr, `known - a memory graph for LLMs

Usage:
  known [global flags] <command> [command flags] [arguments]

Global Flags:
  --dsn <string>    PostgreSQL connection string (env: KNOWN_DSN)
  --json            Output as JSON
  --quiet           Suppress non-essential output

Commands:
  search     Search entries by semantic similarity
  related    Find related entries via graph traversal
  conflicts  Detect conflicting entries
  path       Find shortest path between entries
  link       Create an edge between entries
  unlink     Delete an edge
  scope      Manage scopes (list, create, tree)
  gc         Delete expired entries
  stats      Show knowledge graph statistics
  export     Export entries as JSON or JSONL
  import     Import entries from JSON or JSONL

Run 'known <command> --help' for details on a specific command.
`)
}

// Run is the main entry point for the CLI. It parses args and dispatches
// to the appropriate subcommand.
func Run(ctx context.Context, args []string) int {
	if len(args) < 1 {
		usage()
		return 1
	}

	gf, remaining := parseGlobalFlags(args)

	if len(remaining) == 0 {
		usage()
		return 1
	}

	subcmd := remaining[0]
	subArgs := remaining[1:]

	// Only search needs the embedder to generate query vectors.
	// Graph traversal commands (related, conflicts, path) use the query engine
	// but do not call embedding methods, so they pass nil for the embedder.
	needsEmbedder := subcmd == "search"

	// Help does not need app init.
	if subcmd == "help" || subcmd == "--help" || subcmd == "-h" {
		usage()
		return 0
	}

	app, err := initApp(ctx, gf, needsEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer app.Close()

	switch subcmd {
	case "search":
		err = runSearch(ctx, app, subArgs)
	case "related":
		err = runRelated(ctx, app, subArgs)
	case "conflicts":
		err = runConflicts(ctx, app, subArgs)
	case "path":
		err = runPath(ctx, app, subArgs)
	case "link":
		err = runLink(ctx, app, subArgs)
	case "unlink":
		err = runUnlink(ctx, app, subArgs)
	case "scope":
		err = runScope(ctx, app, subArgs)
	case "gc":
		err = runGC(ctx, app, subArgs)
	case "stats":
		err = runStats(ctx, app, subArgs)
	case "export":
		err = runExport(ctx, app, subArgs)
	case "import":
		err = runImport(ctx, app, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		usage()
		return 1
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
