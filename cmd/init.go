package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// runInit implements the "known init" subcommand.
// It writes a .known.yaml to the current directory, establishing a scope root.
//
// Usage: known init [--dsn <string>] [--force]
func runInit(_ /* ctx */ interface{}, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "PostgreSQL connection string")
	force := fs.Bool("force", false, "overwrite existing .known.yaml")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// DSN from: flag > env > error.
	resolvedDSN := *dsn
	if resolvedDSN == "" {
		resolvedDSN = os.Getenv("KNOWN_DSN")
	}
	if resolvedDSN == "" {
		return fmt.Errorf("DSN required: use --dsn flag or set KNOWN_DSN environment variable")
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
	return nil
}
