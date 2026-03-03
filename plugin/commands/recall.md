---
description: Retrieve knowledge relevant to a query
argument-hint: <query> [--scope <scope>]
---

Retrieve stored knowledge before exploring the codebase. Recall is faster and
cheaper than re-reading source files.

## When to Use

- Before working on a subsystem — recall architecture decisions first
- Before suggesting an approach — check if a decision was already recorded
- When the user asks about conventions, config, or prior decisions
- At session start in a familiar project — recall what's already known

## Instructions

1. Run recall with a natural language query:

```bash
known recall '<query>'
```

2. Synthesize the results into your response. Apply recalled facts (conventions,
   decisions, environment details) to your current work.

3. If no results are returned, tell the user and suggest `/known:search` with
   `--threshold 0.2` to broaden the search.

## Tuning Recall

Start with a plain `known recall '<query>'`. The defaults work well for most cases.

**Raise `--threshold`** when recall returns too many loosely-related results:

```bash
known recall 'auth flow' --threshold 0.5
```

**Raise `--recency`** when you need the most recently observed knowledge:

```bash
known recall 'deployment config' --recency 0.4
```

**Use `--provenance verified`** when you need only confirmed facts (e.g., before
making a breaking change):

```bash
known recall 'API contract' --provenance verified
```

**Use `--source`** to focus on a specific knowledge origin:

```bash
known recall 'config values' --source file
```

## Flag Reference

| Flag | Default | Purpose |
|------|---------|---------|
| `--scope` | auto | Scope to search within |
| `--threshold` | 0.3 | Raise to get fewer, more relevant results |
| `--recency` | 0.1 | Raise to prefer recently observed knowledge |
| `--expand-depth` | 1 | Graph expansion hops from each result |
| `--provenance` | all | `verified`, `inferred`, or `uncertain` |
| `--source` | all | `file`, `url`, `conversation`, or `manual` |
| `--label` | all | Filter by label (repeatable) |
| `--limit` | 20 | Maximum results in scope-listing mode |

## Scope

Scope is auto-derived from your working directory. Override with `--scope`:

```bash
known recall 'query' --scope backend.api
```

Scope search is hierarchical: `backend` includes `backend.api` and all
`backend.*` descendants.

## Recall vs Search

- **`/known:recall`** — Plain text for LLM context. Use by default.
- **`/known:search`** — Scores and optional JSON. Use when you need similarity scores or machine-readable output.
