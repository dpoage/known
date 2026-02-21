# Known — Claude Code Plugin

Persistent memory graph for LLMs. Store facts in one session, recall them in the next.

## Prerequisites

The `known` CLI binary must be installed and available in your `$PATH`.

### Install via Go

```bash
go install github.com/dpoage/known/cmd/known@latest
```

### Install from GitHub Releases

Download a prebuilt binary from [Releases](https://github.com/dpoage/known/releases) and place it in your `$PATH`.

### Verify

```bash
known --version
```

## Install the Plugin

### From marketplace

```bash
claude plugin marketplace add dpoage/known
claude plugin install known@known-marketplace
```

### Local development

```bash
claude --plugin-dir /path/to/known/plugin
```

## Commands

| Command | Purpose |
|---------|---------|
| `/known:remember` | Store a fact from the conversation |
| `/known:recall` | Retrieve knowledge relevant to a query |
| `/known:forget` | Find and delete an entry |
| `/known:search` | Search with full control over flags |

## Plugin vs `known init`

There are two ways to integrate Known with Claude Code:

| Method | When to use |
|--------|-------------|
| **Plugin** (`claude plugin install`) | Shared across all projects, team-installable, marketplace distribution |
| **Standalone** (`known init`) | Per-project setup, skills scoped to one repo, no plugin system needed |

Both provide the same skills. The plugin namespaces them as `/known:remember` while standalone uses `/remember`.

If you've installed the plugin, you don't need to run `known init` for Claude Code integration (though `known init` still creates the `.known.yaml` config file, which you may still want).

## How It Works

On session start, a hook runs `known scope tree` to inject available knowledge scopes into context. Use `/known:recall` to retrieve knowledge before exploring the codebase — it's faster than re-reading files.

Knowledge is stored locally in SQLite (`~/.known/known.db` by default) or PostgreSQL for production use. Entries are embedded for semantic search, organized by hierarchical scopes, and linked via graph edges.
