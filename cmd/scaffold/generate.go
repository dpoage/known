//go:build ignore

// Generate standalone SKILL.md files from plugin command sources.
//
// Plugin commands in plugin/commands/*.md are the canonical source.
// This tool transforms them into standalone skill files at
// cmd/scaffold/templates/skills/<name>/SKILL.md by:
//
//   - Stripping YAML frontmatter
//   - Adding a # /<name> header and ## Usage block
//   - Replacing /known: prefixed references with /
//
// Usage:
//
//	go run cmd/scaffold/generate.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// command defines the mapping from a plugin command file to a standalone skill.
type command struct {
	// Source filename in plugin/commands/ (without .md).
	src string
	// Destination directory name under cmd/scaffold/templates/skills/.
	// Empty means same as src.
	dst string
	// Title suffix for the standalone header (after "/<name> — ").
	title string
}

var commands = []command{
	{src: "remember", title: "Store a fact in known"},
	{src: "recall", title: "Retrieve knowledge from known"},
	{src: "forget", title: "Delete an entry from known"},
	{src: "search", dst: "known-search", title: "Search entries with full control"},
}

func main() {
	root := findRoot()

	for _, cmd := range commands {
		dst := cmd.dst
		if dst == "" {
			dst = cmd.src
		}

		srcPath := filepath.Join(root, "plugin", "commands", cmd.src+".md")
		dstPath := filepath.Join(root, "cmd", "scaffold", "templates", "skills", dst, "SKILL.md")

		if err := generate(srcPath, dstPath, cmd, dst); err != nil {
			fmt.Fprintf(os.Stderr, "error generating %s: %v\n", dst, err)
			os.Exit(1)
		}
		fmt.Printf("generated %s\n", dstPath)
	}
}

func generate(srcPath, dstPath string, cmd command, skillName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var (
		lines        []string
		inFrontmatter bool
		frontmatterDone bool
		argHint      string
		description  string
	)

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Parse frontmatter.
		if lineNum == 1 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
				frontmatterDone = true
				continue
			}
			if strings.HasPrefix(line, "argument-hint:") {
				argHint = strings.TrimSpace(strings.TrimPrefix(line, "argument-hint:"))
			}
			if strings.HasPrefix(line, "description:") {
				description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			}
			continue
		}

		// Skip the blank line immediately after frontmatter.
		if frontmatterDone && line == "" {
			frontmatterDone = false
			continue
		}
		frontmatterDone = false

		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Use frontmatter description as fallback if no explicit title.
	_ = description

	// Build the standalone header + usage block.
	var out strings.Builder
	out.WriteString(fmt.Sprintf("# /%s — %s\n", skillName, cmd.title))
	out.WriteString("\n")

	// First content line is the description paragraph — write it, then add usage block.
	i := 0
	for i < len(lines) && lines[i] != "" {
		out.WriteString(lines[i])
		out.WriteString("\n")
		i++
	}
	out.WriteString("\n")

	// Insert usage block.
	out.WriteString("## Usage\n\n")
	out.WriteString("```\n")
	out.WriteString(fmt.Sprintf("/%s %s\n", skillName, argHint))
	out.WriteString("```\n")
	out.WriteString("\n")

	// Skip blank line between description and next section.
	for i < len(lines) && lines[i] == "" {
		i++
	}

	// Write remaining lines with substitutions.
	for ; i < len(lines); i++ {
		line := lines[i]

		// Replace /known:<name> with standalone skill names.
		// /known:search -> /known-search (standalone name differs).
		// All others: /known:<x> -> /<x>.
		line = strings.ReplaceAll(line, "/known:search", "/known-search")
		line = strings.ReplaceAll(line, "/known:", "/")

		out.WriteString(line)
		out.WriteString("\n")
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dstPath, []byte(out.String()), 0o644)
}

// findRoot walks up from cwd looking for go.mod.
func findRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine working directory:", err)
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fmt.Fprintln(os.Stderr, "cannot find project root (no go.mod found)")
			os.Exit(1)
		}
		dir = parent
	}
}
