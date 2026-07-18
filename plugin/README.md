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

`known init` is optional scaffolding. No `.known.yaml` config file is required — scope is
auto-derived from the project root. If you've installed the plugin, you don't need to run
`known init` for Claude Code integration.
