// Package cmd implements the CLI commands for the known knowledge graph.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// App holds the shared dependencies for all CLI commands.
type App struct {
	DB        storage.Backend
	Entries   storage.EntryRepo
	Edges     storage.EdgeRepo
	Scopes    storage.ScopeRepo
	Sessions  storage.SessionRepo
	SessionID string // active session ID read from ~/.known/session
	Embedder  embed.Embedder
	Engine    *query.Engine
	Printer   *Printer
	Config    *AppConfig
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
	fs.SetOutput(io.Discard)      // suppress default error output
	fs.SetInterspersed(false)     // stop parsing at first non-flag (the subcommand)
	fs.StringVar(&gf.dsn, "dsn", "", "database connection string (default: ~/.known/known.db)")
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

	db, err := newBackend(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := db.Migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Scopes:   db.Scopes(),
		Sessions: db.Sessions(),
		Printer:  NewPrinter(os.Stdout, cfg.JSON, cfg.Quiet),
		Config:   cfg,
	}

	// Read active session ID (best-effort).
	app.SessionID = readSessionID()

	if needsEmbedder {
		embedCfg := embed.LoadConfig()
		embedder, err := embed.NewEmbedder(embedCfg)
		if err != nil {
			_ = db.Close()
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
		_ = a.DB.Close()
	}
}

// usage prints the top-level help message.
func usage() {
	fmt.Fprintf(os.Stderr, `known - a memory graph for LLMs

Usage:
  known [global flags] <command> [command flags] [arguments]

Global Flags:
  --dsn <string>    Database connection string (default: ~/.known/known.db)
  --json            Output as JSON
  --quiet           Suppress non-essential output

Commands:
  init       Initialize a scope root (.known.yaml) in the current directory
  add        Add a new knowledge entry
  update     Update an existing entry
  delete     Delete an entry
  show       Show entry details with relationships
  list       Browse entries by scope, source type, or provenance
  search     Search entries by semantic similarity
  recall     Retrieve knowledge optimized for LLM context
  related    Find related entries via graph traversal
  conflicts  Detect conflicting entries
  path       Find shortest path between entries
  link       Create an edge between entries
  unlink     Delete an edge
  scope      Manage scopes (list, create, delete, tree)
  gc         Delete expired entries and reinforce edges
  session    Manage agent sessions (start, end)
  stats      Show knowledge graph statistics
  export     Export entries as JSON or JSONL
  import     Import entries from JSON or JSONL
  completion Generate shell completions (bash, fish, zsh)
  version    Print version information

Run 'known <command> --help' for details on a specific command.
`)
}

// Run is the main entry point for the CLI. It parses args and dispatches
// to the appropriate subcommand.
func Run(ctx context.Context, args []string) int {
	if len(args) < 1 {
		usage()
		return 0
	}

	// Check for --version before flag parsing (pflag would swallow it).
	// Stop at the first non-flag argument (the subcommand) to avoid
	// hijacking -v or --version from subcommand args.
	for _, a := range args {
		if a == "--version" {
			runVersion()
			return 0
		}
		if a == "--" || !strings.HasPrefix(a, "-") {
			break
		}
	}

	gf, remaining := parseGlobalFlags(args)

	if len(remaining) == 0 {
		usage()
		return 0
	}

	subcmd := remaining[0]
	subArgs := remaining[1:]

	// Commands that generate embeddings need the embedder initialized.
	needsEmbedder := subcmd == "search" || subcmd == "add" || subcmd == "update" || subcmd == "recall"

	// Commands that don't need app init (no DB, no session).
	if subcmd == "help" || subcmd == "--help" || subcmd == "-h" {
		usage()
		return 0
	}

	if subcmd == "version" {
		runVersion()
		return 0
	}

	if subcmd == "init" {
		if err := runInit(ctx, subArgs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	if subcmd == "completion" {
		if err := runCompletion(subArgs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	if subcmd == "__complete" {
		if err := runCompleteLight(ctx, gf, subArgs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	app, err := initApp(ctx, gf, needsEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer app.Close()

	switch subcmd {
	case "add":
		err = runAdd(ctx, app, subArgs)
	case "update":
		err = runUpdate(ctx, app, subArgs)
	case "delete":
		err = runDelete(ctx, app, subArgs)
	case "show":
		err = runShow(ctx, app, subArgs)
	case "list":
		err = runList(ctx, app, subArgs)
	case "search":
		err = runSearch(ctx, app, subArgs)
	case "recall":
		err = runRecall(ctx, app, subArgs)
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
	case "session":
		err = runSession(ctx, app, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		usage()
		return 1
	}

	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Best-effort session event logging after successful commands.
	logSessionEvent(ctx, app, subcmd, subArgs)

	return 0
}
