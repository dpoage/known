//go:build ignore

// Generate standalone templates from plugin sources.
//
// This tool generates two kinds of output:
//
//  1. Per-skill SKILL.md files from plugin/commands/*.md → cmd/scaffold/templates/skills/
//  2. CLAUDE.md from plugin/skills/known/SKILL.md → cmd/scaffold/templates/CLAUDE.md
//
// Both apply the same substitutions: strip YAML frontmatter, replace /known:
// prefixed references with standalone names.
//
// Usage:
//
//	go run cmd/scaffold/generate.go
package main

import (
	"bufio"
	"fmt"
	"io"
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
	{src: "discover", title: "Walk a codebase and store architectural knowledge"},
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

		if err := generateSkill(srcPath, dstPath, cmd, dst); err != nil {
			fmt.Fprintf(os.Stderr, "error generating %s: %v\n", dst, err)
			os.Exit(1)
		}
		fmt.Printf("generated %s\n", dstPath)
	}

	// Generate CLAUDE.md from the master plugin SKILL.md.
	skillSrc := filepath.Join(root, "plugin", "skills", "known", "SKILL.md")
	claudeDst := filepath.Join(root, "cmd", "scaffold", "templates", "CLAUDE.md")
	if err := generateCLAUDE(skillSrc, claudeDst); err != nil {
		fmt.Fprintf(os.Stderr, "error generating CLAUDE.md: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("generated %s\n", claudeDst)
}

// stripFrontmatter reads a markdown file, strips YAML frontmatter and the
// blank line following it, and returns the remaining content lines along with
// any key-value pairs found in the frontmatter block.
func stripFrontmatter(r io.Reader) (lines []string, meta map[string]string, err error) {
	meta = make(map[string]string)
	var (
		inFrontmatter bool
		skipNextBlank bool
	)

	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		if lineNum == 1 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
				skipNextBlank = true
				continue
			}
			if i := strings.Index(line, ":"); i > 0 {
				meta[line[:i]] = strings.TrimSpace(line[i+1:])
			}
			continue
		}
		if skipNextBlank && line == "" {
			skipNextBlank = false
			continue
		}
		skipNextBlank = false

		lines = append(lines, line)
	}
	return lines, meta, scanner.Err()
}

// applySubstitutions replaces /known: prefixed references with standalone names.
// The search-specific rule must precede the general rule to avoid corruption.
func applySubstitutions(line string) string {
	line = strings.ReplaceAll(line, "/known:search", "/known-search")
	line = strings.ReplaceAll(line, "/known:", "/")
	return line
}

// writeFile creates parent directories and writes content atomically.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func generateSkill(srcPath, dstPath string, cmd command, skillName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	lines, meta, err := stripFrontmatter(f)
	if err != nil {
		return err
	}
	argHint := meta["argument-hint"]

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
		out.WriteString(applySubstitutions(lines[i]))
		out.WriteString("\n")
	}

	return writeFile(dstPath, out.String())
}

// generateCLAUDE transforms the master plugin SKILL.md into a standalone CLAUDE.md.
// It strips frontmatter, replaces /known: prefixes with standalone names, and
// renames "Commands" to "Skills" in the table.
func generateCLAUDE(srcPath, dstPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	lines, _, err := stripFrontmatter(f)
	if err != nil {
		return err
	}

	var out strings.Builder
	inSkillsSection := false
	for _, line := range lines {
		line = applySubstitutions(line)

		// Rename the "Commands" section (skill listing) to "Skills".
		// Only rename the table header in that section, not in "Other Useful CLI Commands".
		if line == "## Commands" {
			line = "## Skills"
			inSkillsSection = true
		} else if strings.HasPrefix(line, "## ") {
			inSkillsSection = false
		}
		if inSkillsSection {
			line = strings.ReplaceAll(line, "| Command |", "| Skill |")
			line = strings.ReplaceAll(line, "|---------|", "|-------|")
		}

		out.WriteString(line)
		out.WriteString("\n")
	}

	return writeFile(dstPath, out.String())
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
