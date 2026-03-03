---
description: Retrieve knowledge relevant to a query
argument-hint: <query> [flags]
---

Retrieve knowledge relevant to a query, optimized for LLM context.

Use this **before exploring the codebase** when working in an area where knowledge may
already exist. Recalling stored decisions, conventions, and environment facts is faster
than re-reading source files.

## When to Use

- About to work on a subsystem — recall architecture decisions first
- User asks about conventions, config, or prior decisions
- Starting a new session in a familiar project — recall what's already known
- Before suggesting an approach — check if a decision was already recorded

## Instructions

1. Run the recall command with the user's query:

```bash
known recall '<query>'
```

2. Use the results to inform your work. If the user asked a question, answer it using the recalled knowledge. If you're performing a task, apply the recalled facts (conventions, decisions, environment details) to what you're doing.

3. Briefly tell the user what you found, but don't just dump raw output — synthesize it into your response.

4. If no results are returned, tell the user no matching knowledge was found. Suggest `/known:search` with a lower `--threshold` to broaden the search.

### Available flags

| Flag | Default | Description |
|------|---------|-------------|
| `--scope` | auto | Scope to search within |
| `--limit` | 20 | Maximum results (scope-listing mode) |
| `--threshold` | 0.3 | Minimum similarity score (0-1) |
| `--recency` | 0.1 | Recency weight (0=similarity only, 1=recency only) |
| `--expand-depth` | 1 | Graph expansion depth (hops from each vector result) |
| `--provenance` | | Filter by provenance level: `verified`, `inferred`, `uncertain` |
| `--source` | | Filter by source type: `file`, `url`, `conversation`, `manual` |
| `--label` | | Filter by label (repeatable, AND semantics) |

## Scope

The scope is auto-derived from the current working directory. To search a different scope, pass `--scope`:

```bash
known recall '<query>' --scope backend.api
```

Scope search is hierarchical: searching `backend` includes `backend.api` and all
other `backend.*` descendants.

## Examples

```bash
known recall 'database connection pooling config'
known recall 'authentication flow' --scope backend
known recall 'deployment process'
known recall 'API conventions and patterns' --scope backend.api
known recall 'auth decisions' --provenance verified
known recall 'config values' --source file --threshold 0.5
known recall 'patterns' --provenance inferred --recency 0.3
```

## Recall vs Search

- **`/known:recall`** — Quick retrieval, plain text output tuned for LLM context. Use by default. Supports filtering and tuning via flags.
- **`/known:search`** — Full control: `--limit`, `--threshold`, `--hybrid`, `--json`. Use when you need entry IDs (for show/update/delete) or fine-grained results.
