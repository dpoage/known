package cmd

import (
	flag "github.com/spf13/pflag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dpoage/known/cmd/scaffold"
	"gopkg.in/yaml.v3"
)

// runInit implements the "known init" subcommand.
// It writes a .known.yaml to the current directory, establishing a scope root.
//
// Usage: known init [--dsn <string>] [--force] [--no-scaffold]
func runInit(_ /* ctx */ interface{}, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "database connection string (default: ~/.known/known.db)")
	force := fs.Bool("force", false, "overwrite existing .known.yaml")
	noScaffold := fs.Bool("no-scaffold", false, "skip Claude Code skill scaffolding")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// DSN from: flag > env > default SQLite path.
	resolvedDSN := *dsn
	if resolvedDSN == "" {
		resolvedDSN = os.Getenv("KNOWN_DSN")
	}
	if resolvedDSN == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		resolvedDSN = filepath.Join(home, ".known", "known.db")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	target := filepath.Join(cwd, projectConfigFile)

	if !*force {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", projectConfigFile)
		}
	}

	cfg := projectConfig{DSN: resolvedDSN}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", projectConfigFile, err)
	}

	fmt.Fprintf(os.Stderr, "Initialized %s in %s\n", projectConfigFile, cwd)

	if !*noScaffold {
		if err := scaffold.Install(cwd); err != nil {
			return fmt.Errorf("scaffold Claude Code skills: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Scaffolded Claude Code skills in %s/.claude/\n", cwd)
	}

	return nil
}
