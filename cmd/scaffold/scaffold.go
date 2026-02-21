// Package scaffold installs Claude Code skills and guidance into a project.
//
// Templates are embedded in the binary and written to .claude/ by Install.
// Standalone SKILL.md files are generated from the plugin command sources
// in plugin/commands/ — run "go generate ./cmd/scaffold" to regenerate.
//
//go:generate go run generate.go
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed templates/*
var templates embed.FS

// Install writes Claude Code skills and guidance into dir/.claude/.
// Skill files are never overwritten (idempotent). The CLAUDE.md file is
// written only if it does not already exist.
func Install(dir string) error {
	claudeDir := filepath.Join(dir, ".claude")

	return fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute the destination path relative to .claude/.
		rel, err := filepath.Rel("templates", path)
		if err != nil {
			return err
		}
		dest := filepath.Join(claudeDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		// Skip files that already exist (idempotent).
		if _, err := os.Stat(dest); err == nil {
			return nil
		}

		data, err := templates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		return os.WriteFile(dest, data, 0o644)
	})
}
