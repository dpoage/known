---
name: known
description: >
  Persistent memory graph for LLMs. Store facts during one session, recall them
  in the next. Semantic search, graph relationships, and hierarchical scopes.
allowed-tools: "Read,Bash(known:*)"
version: "0.1.0"
author: "Dustin Poage <https://github.com/dpoage>"
license: "MIT"
---

# Known — Persistent Memory Graph for LLMs

Store knowledge across sessions so agents avoid re-learning the same things.
Entries are embedded for semantic search, organized by scope, and linked via graph edges.

## Prerequisites

```bash
known --version  # Requires known CLI in PATH
```

Install: `go install github.com/dpoage/known/cmd/known@latest`

## Commands

| Command | Purpose |
|---------|---------|
| `/known:remember` | Store a fact from the conversation |
| `/known:recall` | Retrieve knowledge relevant to a query |
| `/known:forget` | Find and delete an entry |
| `/known:search` | Search with full control over flags |

## When to Recall

Use `/known:recall` when you're about to explore or make decisions in an area where
knowledge may already exist. Recall is cheaper than re-reading source files.

```bash
known recall 'deployment process'
known recall 'database schema decisions' --scope backend
```

Recall is scoped — it searches within the scope auto-derived from your working directory.
Override with `--scope` to search a different area.

## When to Store

Use `/known:remember` proactively when the user:
- Makes a decision ("we chose Postgres over SQLite because...")
- States an environment fact ("staging DB is at 10.0.1.5")
- Corrects a misconception ("the API returns 201, not 200")
- Expresses a preference ("always use tabs, not spaces")
- Shares tribal knowledge not captured in code or docs

Also store conclusions from codebase exploration — architecture patterns, key file
locations, non-obvious conventions. These save future sessions from re-indexing.

You don't need to ask permission — just store it and tell the user what you remembered.

## When NOT to Store

- Ephemeral context (current task steps, debugging state)
- Information already in code or docs (don't duplicate the repo)
- Speculative or unverified conclusions
- Obvious facts derivable from the codebase

## Content Discipline

Each entry should be a single, atomic fact. Prefer:

```
"The payments service rate-limits to 100 req/s per tenant"
```

Over:

```
"The payments service has rate limiting. It's 100 req/s. This is per tenant."
```

## Scopes

Scopes organize knowledge hierarchically. The scope is auto-derived from your
working directory relative to the `.known.yaml` root.

- `root` — project-wide knowledge
- `backend` — backend-specific (from `./backend/`)
- `backend.api` — API layer (from `./backend/api/`)

Scope search is hierarchical: searching `backend` includes `backend.api` and all
other `backend.*` descendants.

## Recall vs Search

- **`/known:recall`** — Quick retrieval, plain text, tuned for LLM context. Use by default.
- **`/known:search`** — Full control: `--limit`, `--threshold`, `--hybrid`. Use when you need entry IDs or fine-grained results.

## Other Useful CLI Commands

These are available directly (not as plugin commands):

| Command | Purpose |
|---------|---------|
| `known show <id>` | Full entry details with relationships |
| `known update <id> --content '...'` | Modify an entry |
| `known related <id>` | Find connected entries via graph edges |
| `known link <from> <to> --type <type>` | Create a relationship |
| `known conflicts` | Detect contradictory entries |
| `known stats` | Knowledge graph statistics |

## Source Types

| Type | When |
|------|------|
| `conversation` | Facts from chat with the user |
| `file` | Extracted from a source file |
| `url` | From a web page or API doc |
| `manual` | User entered directly |

## Confidence Levels

| Level | When |
|-------|------|
| `verified` | User confirmed or from authoritative source |
| `inferred` | Reasonable conclusion from context |
| `uncertain` | Might be wrong, needs verification |

## Global Flags

These go **before** the subcommand:

```bash
known --json search 'query'    # JSON output
known --quiet recall 'query'   # suppress non-essential output
```
