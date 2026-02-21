# Known — LLM Memory Graph

This project uses [known](https://github.com/dpoage/known) for persistent knowledge storage.
Knowledge persists across sessions — use it to avoid re-learning the same things.

## Scope Tree (session context)

At session start, a hook injects the scope tree from known. If you see scope names
in your context (e.g., `root`, `backend`, `backend.api`), that means knowledge exists
for those areas. Use `/recall` to retrieve it before exploring the codebase yourself.

Example: if you see scope `backend.api` and you're about to work on an API endpoint,
recall what's already known:

```bash
known recall 'API conventions and patterns' --scope backend.api
```

If no scope tree appeared, the knowledge graph may be empty — proceed normally.

## Skills

| Skill | Purpose |
|-------|---------|
| `/remember` | Store a fact from the conversation |
| `/recall` | Retrieve knowledge relevant to a query |
| `/forget` | Find and delete an entry |
| `/known-search` | Search with full control over flags |

## When to Recall

Use `/recall` when you're about to explore or make decisions in an area where
knowledge may already exist. Recall is cheaper than re-reading source files.

```bash
known recall 'deployment process'
known recall 'database schema decisions' --scope backend
```

Recall is scoped — it searches within the scope auto-derived from your working directory.
Override with `--scope` to search a different area.

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
working directory relative to the `.known.yaml` root.

- `root` — project-wide knowledge
- `backend` — backend-specific (from `./backend/`)
- `backend.api` — API layer (from `./backend/api/`)

Scope search is hierarchical: searching `backend` includes `backend.api` and all
other `backend.*` descendants.

Override with `--scope` when the auto-derived scope isn't appropriate.

### Cross-project recall

If multiple projects share the same database (the default `~/.known/known.db`),
you can recall knowledge from another project by specifying its scope:

```bash
known recall 'auth token format' --scope otherproject
```

This only works when both projects use the same DB. If a project has a per-project
DSN in its `.known.yaml`, its knowledge is isolated to that database.

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

## TTL Guidance

- Conversation facts: default TTL applies (see `.known.yaml`)
- Stable architecture decisions: omit TTL (permanent)
- Temporary workarounds: set short TTL (e.g., `168h` = 1 week)

## Recall vs Search

- **`/recall`** — Quick retrieval, plain text, tuned for LLM context. Use by default. Results inform your work — don't just display them.
- **`/known-search`** — Full control: `--limit`, `--threshold`, `--hybrid`. Use when you need entry IDs (for show/update/delete) or fine-grained results.

## Other Useful Commands

These aren't skills but are available directly:

| Command | Purpose |
|---------|---------|
| `known show <id>` | Full entry details with relationships |
| `known update <id> --content '...'` | Modify an entry's content, confidence, or scope |
| `known related <id>` | Find entries connected via graph edges |
| `known link <from-id> <to-id> --type <type>` | Create a relationship between entries |
| `known conflicts` | Detect contradictory entries |
| `known stats` | Knowledge graph statistics |

## Global Flags

These go **before** the subcommand:

```bash
known --json search 'query'    # JSON output
known --quiet recall 'query'   # suppress non-essential output
```
