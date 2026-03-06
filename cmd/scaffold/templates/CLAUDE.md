# Known — Persistent Memory Graph for LLMs

Store knowledge across sessions so agents avoid re-learning the same things.
Entries are embedded for semantic search, organized by scope, and linked via graph edges.

## Prerequisites

```bash
known --version  # Requires known CLI in PATH
```

Install: `go install github.com/dpoage/known/cmd/known@latest`

## Skills

| Skill | Purpose |
|-------|---------|
| `/remember` | Store a fact from the conversation |
| `/recall` | Retrieve knowledge relevant to a query |
| `/forget` | Find and delete an entry |
| `/known-search` | Search with full control over flags |
| `/discover` | Walk a codebase and store architectural knowledge |

## When to Recall

Recall before exploring. If you're about to read files or make decisions in an
area where knowledge may exist, recall first — it's faster than re-reading source.

```bash
known recall 'deployment process'
known recall 'database schema decisions' --scope backend
```

Recall is scoped to your working directory. Override with `--scope` to search
a different area. The defaults work well — add flags only when tuning results.

## When to Store

Use `/remember` proactively when the user:
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
working directory relative to the `.known.yaml` root. In `known scope tree`
output, root scopes are prefixed with `/` to mark project boundaries. Within
your own project, use bare scope names. The `/` prefix is for cross-project
access only.

- `root` — project-wide knowledge
- `backend` — backend-specific (from `./backend/`)
- `backend.api` — API layer (from `./backend/api/`)

Scope search is hierarchical: searching `backend` includes `backend.api` and all
other `backend.*` descendants.

## Recall vs Search

- **`/recall`** — Plain text output tuned for LLM context. Supports filtering and tuning via flags. Use by default.
- **`/known-search`** — Structured output with scores and optional JSON. Use when you need exact similarity scores or machine-readable results.

## Other Useful CLI Commands

These are available directly (not as plugin commands):

| Command | Purpose |
|---------|---------|
| `known show <id>` | Full entry details with relationships |
| `known update <id> --content '...'` | Modify an entry |
| `known related <id>` | Find connected entries via graph edges |
| `known link <from> <to> --type <type>` | Create a relationship (standalone) |
| `known add '...' --link type:id` | Create entry + edge atomically |
| `known conflicts` | Detect contradictory entries |
| `known stats` | Knowledge graph statistics |

## Source Types

| Type | When |
|------|------|
| `conversation` | Facts from chat with the user |
| `file` | Extracted from a source file |
| `url` | From a web page or API doc |
| `manual` | User entered directly |

## Provenance Levels

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
