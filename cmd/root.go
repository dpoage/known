package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/postgres"
)

// App holds the initialized dependencies shared across subcommands.
type App struct {
	DB       *postgres.DB
	Entries  storage.EntryRepo
	Edges    storage.EdgeRepo
	Scopes   storage.ScopeRepo
	Embedder embed.Embedder
	Query    *query.Engine
	Printer  *Printer
	Config   Config
}

// subcommand maps a name to its handler function.
type subcommand struct {
	fn    func(ctx context.Context, app *App, args []string) error
	usage string
}

// Run parses global flags, initializes the application, and dispatches to
// the appropriate subcommand. It returns an error rather than calling
// os.Exit, leaving that to the caller in main.
func Run(args []string) error {
	// Global flags.
	globalFlags := flag.NewFlagSet("known", flag.ContinueOnError)
	dsnFlag := globalFlags.String("dsn", "", "PostgreSQL connection string (env: KNOWN_DSN)")
	jsonFlag := globalFlags.Bool("json", false, "output as JSON")
	quietFlag := globalFlags.Bool("quiet", false, "minimal output (IDs only)")

	globalFlags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: known [flags] <command> [command-flags] [args]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  add       Add a new knowledge entry\n")
		fmt.Fprintf(os.Stderr, "  update    Update an existing entry\n")
		fmt.Fprintf(os.Stderr, "  delete    Delete an entry\n")
		fmt.Fprintf(os.Stderr, "  show      Show entry details with relationships\n")
		fmt.Fprintf(os.Stderr, "\nGlobal flags:\n")
		globalFlags.PrintDefaults()
	}

	if err := globalFlags.Parse(args); err != nil {
		return err
	}

	remaining := globalFlags.Args()
	if len(remaining) == 0 {
		globalFlags.Usage()
		return fmt.Errorf("no command specified")
	}

	cmdName := remaining[0]
	cmdArgs := remaining[1:]

	// Load config from file/env, then apply flag overrides.
	cfg := LoadConfig()
	if *dsnFlag != "" {
		cfg.DSN = *dsnFlag
	}
	if *jsonFlag {
		cfg.JSON = true
	}
	if *quietFlag {
		cfg.Quiet = true
	}

	// Determine output mode.
	mode := OutputHuman
	if cfg.JSON {
		mode = OutputJSON
	} else if cfg.Quiet {
		mode = OutputQuiet
	}

	commands := map[string]subcommand{
		"add":    {fn: runAdd, usage: "known add 'content' [flags]"},
		"update": {fn: runUpdate, usage: "known update <id> [flags]"},
		"delete": {fn: runDelete, usage: "known delete <id> [flags]"},
		"show":   {fn: runShow, usage: "known show <id>"},
	}

	sub, ok := commands[cmdName]
	if !ok {
		globalFlags.Usage()
		return fmt.Errorf("unknown command: %s", cmdName)
	}

	// Initialize dependencies.
	ctx := context.Background()

	db, err := postgres.New(ctx, postgres.Config{DSN: cfg.DSN})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	if err := db.Migrate(cfg.DSN); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	embedCfg := embed.LoadConfig()
	embedder, err := embed.NewEmbedder(embedCfg)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}

	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Scopes:   db.Scopes(),
		Embedder: embedder,
		Query:    query.New(db.Entries(), db.Edges(), embedder),
		Printer:  NewPrinter(os.Stdout, mode),
		Config:   cfg,
	}

	if err := sub.fn(ctx, app, cmdArgs); err != nil {
		return fmt.Errorf("%s: %w", cmdName, err)
	}

	return nil
}
