package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// flagDef describes a single CLI flag for completion generation.
type flagDef struct {
	name    string   // long flag name (without --)
	short   string   // short flag letter (without -)
	values  []string // enum values, nil = free-form
	dynamic string   // completion type for runtime query (e.g. "scopes"), empty = not dynamic
}

// cmdDef describes a subcommand for completion generation.
type cmdDef struct {
	name  string   // subcommand name
	desc  string   // short description
	flags []flagDef
	subs  []string // sub-subcommands (only scope has these)
}

// commands is the complete registry of known CLI subcommands and their flags.
var commands = []cmdDef{
	{name: "init", desc: "Initialize a scope root (.known.yaml)", flags: []flagDef{
		{name: "dsn"},
		{name: "force"},
		{name: "no-scaffold"},
	}},
	{name: "add", desc: "Add a new knowledge entry", flags: []flagDef{
		{name: "title"},
		{name: "scope", dynamic: "scopes"},
		{name: "source-type", values: []string{"file", "url", "conversation", "manual"}},
		{name: "source-ref"},
		{name: "confidence", values: []string{"verified", "inferred", "uncertain"}},
		{name: "ttl"},
		{name: "meta"},
		{name: "link"},
	}},
	{name: "update", desc: "Update an existing entry", flags: []flagDef{
		{name: "title"},
		{name: "content"},
		{name: "confidence", values: []string{"verified", "inferred", "uncertain"}},
		{name: "scope", dynamic: "scopes"},
		{name: "source-type", values: []string{"file", "url", "conversation", "manual"}},
		{name: "source-ref"},
		{name: "ttl"},
		{name: "meta"},
	}},
	{name: "delete", desc: "Delete an entry", flags: []flagDef{
		{name: "force", short: "f"},
	}},
	{name: "show", desc: "Show entry details with relationships", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
		{name: "limit"},
	}},
	{name: "list", desc: "Browse entries by scope, source type, or confidence", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
		{name: "source-type", values: []string{"file", "url", "conversation", "manual"}},
		{name: "confidence", values: []string{"verified", "inferred", "uncertain"}},
		{name: "limit"},
		{name: "json"},
	}},
	{name: "search", desc: "Search entries by semantic similarity", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
		{name: "limit"},
		{name: "threshold"},
		{name: "recency"},
		{name: "hybrid"},
		{name: "expand-depth"},
	}},
	{name: "recall", desc: "Retrieve knowledge optimized for LLM context", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
		{name: "limit"},
	}},
	{name: "related", desc: "Find related entries via graph traversal", flags: []flagDef{
		{name: "depth"},
		{name: "direction", values: []string{"out", "outgoing", "in", "incoming", "both"}},
		{name: "edge-type"},
	}},
	{name: "conflicts", desc: "Detect conflicting entries", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
	}},
	{name: "path", desc: "Find shortest path between entries", flags: []flagDef{
		{name: "max-depth"},
	}},
	{name: "link", desc: "Create an edge between entries", flags: []flagDef{
		{name: "type", values: []string{"depends-on", "contradicts", "supersedes", "elaborates", "related-to"}},
		{name: "weight"},
		{name: "meta"},
	}},
	{name: "unlink", desc: "Delete an edge"},
	{name: "scope", desc: "Manage scopes (list, create, tree)", subs: []string{"list", "create", "tree"}},
	{name: "gc", desc: "Delete expired entries"},
	{name: "stats", desc: "Show knowledge graph statistics", flags: []flagDef{
		{name: "scope", dynamic: "scopes"},
	}},
	{name: "export", desc: "Export entries as JSON or JSONL", flags: []flagDef{
		{name: "format", values: []string{"json", "jsonl"}},
		{name: "scope", dynamic: "scopes"},
	}},
	{name: "import", desc: "Import entries from JSON or JSONL"},
	{name: "completion", desc: "Generate shell completions (bash, fish, zsh)"},
}

// globalFlagDefs are the flags available before any subcommand.
var globalFlagDefs = []flagDef{
	{name: "dsn"},
	{name: "json"},
	{name: "quiet"},
}

// runCompletion implements the "known completion" subcommand.
func runCompletion(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: known completion <shell>\n\nSupported shells: bash, fish, zsh")
	}

	shell := args[0]
	var script string

	switch shell {
	case "bash":
		script = generateBash()
	case "fish":
		script = generateFish()
	case "zsh":
		script = generateZsh()
	default:
		return fmt.Errorf("unsupported shell %q: must be bash, fish, or zsh", shell)
	}

	fmt.Fprint(os.Stdout, script)
	return nil
}

// runComplete implements the hidden "known __complete <type>" subcommand.
// It queries the DB and prints completion candidates one per line.
func runComplete(ctx context.Context, app *App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: known __complete <type>")
	}
	switch args[0] {
	case "scopes":
		scopes, err := app.Scopes.List(ctx)
		if err != nil {
			return err
		}
		for _, s := range scopes {
			fmt.Fprintln(os.Stdout, s.Path)
		}
		return nil
	default:
		return fmt.Errorf("unknown completion type: %s", args[0])
	}
}

// generateBash produces a bash completion script.
func generateBash() string {
	var b strings.Builder

	b.WriteString("# bash completion for known\n")
	b.WriteString("_known_completions() {\n")
	b.WriteString("    local cur prev words cword\n")
	b.WriteString("    _init_completion || return\n")
	b.WriteString("\n")

	// Determine which word is the subcommand (skip global flags).
	b.WriteString("    # Find the subcommand, skipping global flags and their values.\n")
	b.WriteString("    local subcmd=\"\" subcmd_idx=0\n")
	b.WriteString("    local i=1\n")
	b.WriteString("    while [[ $i -lt $cword ]]; do\n")
	b.WriteString("        case \"${words[$i]}\" in\n")
	b.WriteString("            --dsn)\n")
	b.WriteString("                ((i+=2));;\n")
	b.WriteString("            --json|--quiet)\n")
	b.WriteString("                ((i++));;\n")
	b.WriteString("            -*)\n")
	b.WriteString("                ((i++));;\n")
	b.WriteString("            *)\n")
	b.WriteString("                subcmd=\"${words[$i]}\"\n")
	b.WriteString("                subcmd_idx=$i\n")
	b.WriteString("                break;;\n")
	b.WriteString("        esac\n")
	b.WriteString("    done\n")
	b.WriteString("\n")

	// If no subcommand yet, complete subcommand names + global flags.
	b.WriteString("    if [[ -z \"$subcmd\" ]]; then\n")
	b.WriteString("        local commands=\"")
	for i, cmd := range commands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(cmd.name)
	}
	b.WriteString("\"\n")
	b.WriteString("        local global_flags=\"--dsn --json --quiet\"\n")
	b.WriteString("        if [[ \"$cur\" == -* ]]; then\n")
	b.WriteString("            COMPREPLY=( $(compgen -W \"$global_flags\" -- \"$cur\") )\n")
	b.WriteString("        else\n")
	b.WriteString("            COMPREPLY=( $(compgen -W \"$commands\" -- \"$cur\") )\n")
	b.WriteString("        fi\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n")
	b.WriteString("\n")

	// Per-subcommand completions.
	b.WriteString("    case \"$subcmd\" in\n")
	for _, cmd := range commands {
		if len(cmd.flags) == 0 && len(cmd.subs) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("        %s)\n", cmd.name))

		// Handle scope sub-subcommands.
		if len(cmd.subs) > 0 {
			b.WriteString("            # Sub-subcommand completion.\n")
			b.WriteString("            local sub_subcmd=\"\"\n")
			b.WriteString("            if [[ $((subcmd_idx + 1)) -lt $cword ]]; then\n")
			b.WriteString("                sub_subcmd=\"${words[$((subcmd_idx + 1))]}\"\n")
			b.WriteString("            fi\n")
			b.WriteString("            if [[ -z \"$sub_subcmd\" ]] && [[ \"$cur\" != -* ]]; then\n")
			b.WriteString(fmt.Sprintf("                COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n", strings.Join(cmd.subs, " ")))
			b.WriteString("                return\n")
			b.WriteString("            fi\n")
		}

		if len(cmd.flags) > 0 {
			// Check if prev is a flag with completable values (enum or dynamic).
			hasCompletableFlags := false
			for _, f := range cmd.flags {
				if len(f.values) > 0 || f.dynamic != "" {
					hasCompletableFlags = true
					break
				}
			}
			if hasCompletableFlags {
				b.WriteString("            case \"$prev\" in\n")
				for _, f := range cmd.flags {
					if len(f.values) > 0 {
						b.WriteString(fmt.Sprintf("                --%s)\n", f.name))
						b.WriteString(fmt.Sprintf("                    COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n", strings.Join(f.values, " ")))
						b.WriteString("                    return;;\n")
					} else if f.dynamic != "" {
						b.WriteString(fmt.Sprintf("                --%s)\n", f.name))
						b.WriteString(fmt.Sprintf("                    COMPREPLY=( $(compgen -W \"$(known __complete %s 2>/dev/null)\" -- \"$cur\") )\n", f.dynamic))
						b.WriteString("                    return;;\n")
					}
				}
				b.WriteString("            esac\n")
			}

			// Complete flag names.
			b.WriteString("            if [[ \"$cur\" == -* ]]; then\n")
			b.WriteString("                COMPREPLY=( $(compgen -W \"")
			for i, f := range cmd.flags {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString("--" + f.name)
				if f.short != "" {
					b.WriteString(" -" + f.short)
				}
			}
			b.WriteString("\" -- \"$cur\") )\n")
			b.WriteString("            fi\n")
		}

		b.WriteString("            ;;\n")
	}
	b.WriteString("    esac\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("complete -F _known_completions known\n")

	return b.String()
}

// generateFish produces a fish completion script.
func generateFish() string {
	var b strings.Builder

	b.WriteString("# fish completion for known\n\n")
	b.WriteString("# Disable file completions by default.\n")
	b.WriteString("complete -c known -f\n\n")

	// Global flags.
	b.WriteString("# Global flags.\n")
	for _, f := range globalFlagDefs {
		b.WriteString(fmt.Sprintf("complete -c known -l %s\n", f.name))
	}
	b.WriteString("\n")

	// Subcommand names.
	b.WriteString("# Subcommands.\n")
	for _, cmd := range commands {
		b.WriteString(fmt.Sprintf("complete -c known -n __fish_use_subcommand -a %s -d %q\n", cmd.name, cmd.desc))
	}
	b.WriteString("\n")

	// Per-subcommand flags.
	b.WriteString("# Per-command flags.\n")
	for _, cmd := range commands {
		if len(cmd.flags) == 0 && len(cmd.subs) == 0 {
			continue
		}
		cond := fmt.Sprintf("__fish_seen_subcommand_from %s", cmd.name)

		// Sub-subcommands (scope).
		for _, sub := range cmd.subs {
			b.WriteString(fmt.Sprintf("complete -c known -n '%s; and not __fish_seen_subcommand_from %s' -a %s\n",
				cond, strings.Join(cmd.subs, " "), sub))
		}

		for _, f := range cmd.flags {
			if len(f.values) > 0 {
				b.WriteString(fmt.Sprintf("complete -c known -n '%s' -l %s -ra '%s'\n",
					cond, f.name, strings.Join(f.values, " ")))
			} else if f.dynamic != "" {
				b.WriteString(fmt.Sprintf("complete -c known -n '%s' -l %s -ra '(known __complete %s 2>/dev/null)'\n",
					cond, f.name, f.dynamic))
			} else {
				short := ""
				if f.short != "" {
					short = fmt.Sprintf(" -s %s", f.short)
				}
				b.WriteString(fmt.Sprintf("complete -c known -n '%s' -l %s%s\n",
					cond, f.name, short))
			}
		}
	}

	return b.String()
}

// generateZsh produces a zsh completion script.
func generateZsh() string {
	var b strings.Builder

	b.WriteString("#compdef known\n\n")

	// Emit helper functions for dynamic completion types.
	dynamicTypes := map[string]bool{}
	for _, cmd := range commands {
		for _, f := range cmd.flags {
			if f.dynamic != "" {
				dynamicTypes[f.dynamic] = true
			}
		}
	}
	for dtype := range dynamicTypes {
		b.WriteString(fmt.Sprintf("__known_complete_%s() {\n", dtype))
		b.WriteString(fmt.Sprintf("    local -a vals\n"))
		b.WriteString(fmt.Sprintf("    vals=(${(f)\"$(known __complete %s 2>/dev/null)\"})\n", dtype))
		b.WriteString(fmt.Sprintf("    compadd -a vals\n"))
		b.WriteString(fmt.Sprintf("}\n\n"))
	}

	b.WriteString("_known() {\n")
	b.WriteString("    local -a global_flags\n")
	b.WriteString("    global_flags=(\n")
	b.WriteString("        '--dsn[Database connection string]:dsn:'\n")
	b.WriteString("        '--json[Output as JSON]'\n")
	b.WriteString("        '--quiet[Suppress non-essential output]'\n")
	b.WriteString("    )\n")
	b.WriteString("\n")

	// Subcommand list.
	b.WriteString("    local -a subcmds\n")
	b.WriteString("    subcmds=(\n")
	for _, cmd := range commands {
		b.WriteString(fmt.Sprintf("        '%s:%s'\n", cmd.name, zshEscape(cmd.desc)))
	}
	b.WriteString("    )\n")
	b.WriteString("\n")

	b.WriteString("    _arguments -C \\\n")
	b.WriteString("        $global_flags \\\n")
	b.WriteString("        '1:command:->cmd' \\\n")
	b.WriteString("        '*::arg:->args'\n")
	b.WriteString("\n")
	b.WriteString("    case $state in\n")
	b.WriteString("    cmd)\n")
	b.WriteString("        _describe 'command' subcmds\n")
	b.WriteString("        ;;\n")
	b.WriteString("    args)\n")
	b.WriteString("        case ${words[1]} in\n")

	for _, cmd := range commands {
		if len(cmd.flags) == 0 && len(cmd.subs) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("        %s)\n", cmd.name))

		if len(cmd.subs) > 0 {
			// Two-level dispatch for scope.
			b.WriteString("            local -a sub_subcmds\n")
			b.WriteString("            sub_subcmds=(")
			for i, sub := range cmd.subs {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString(sub)
			}
			b.WriteString(")\n")
			b.WriteString("            _arguments -C \\\n")
			b.WriteString("                '1:subcommand:(${sub_subcmds})' \\\n")
			b.WriteString("                '*::arg:->sub_args'\n")
		}

		if len(cmd.flags) > 0 {
			b.WriteString("            _arguments \\\n")
			for i, f := range cmd.flags {
				cont := " \\"
				if i == len(cmd.flags)-1 {
					cont = ""
				}
				if len(f.values) > 0 {
					b.WriteString(fmt.Sprintf("                '--%s[%s]:value:(%s)'%s\n",
						f.name, f.name, strings.Join(f.values, " "), cont))
				} else if f.dynamic != "" {
					b.WriteString(fmt.Sprintf("                '--%s[%s]:%s:__known_complete_%s'%s\n",
						f.name, f.name, f.name, f.dynamic, cont))
				} else if f.short != "" {
					b.WriteString(fmt.Sprintf("                {-%s,--%s}'[%s]'%s\n",
						f.short, f.name, f.name, cont))
				} else {
					b.WriteString(fmt.Sprintf("                '--%s[%s]'%s\n",
						f.name, f.name, cont))
				}
			}
		}

		b.WriteString("            ;;\n")
	}

	b.WriteString("        esac\n")
	b.WriteString("        ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("_known\n")

	return b.String()
}

// zshEscape escapes single quotes for zsh completion descriptions.
func zshEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}
