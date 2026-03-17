---
description: Retrieve knowledge relevant to a query
argument-hint: <query> [--scope <scope>]
---

Retrieve stored knowledge. Recall uses hybrid search (vector + full-text + graph
expansion) by default — it is the most comprehensive retrieval mode.

## Instructions

1. Run recall with a natural language query:

```bash
known recall '<query>'
```

2. Synthesize the results into your response. Apply recalled facts (conventions,
   decisions, environment details) to your current work.

3. If no results are returned, proceed normally — the knowledge graph may not
   have entries in this area yet. Optionally try `--text '<exact phrase>'` for
   keyword matching or `--threshold 0.2` to broaden similarity.

## Modes

- **Query mode** (default): `known recall '<query>'` — semantic + text + graph search.
- **Scope listing**: `known recall --scope <path>` (no query) — lists all entries in a scope.

## Tuning Recall

The defaults work well for most cases. Add flags only when tuning results.

**Raise `--threshold`** when recall returns too many loosely-related results:

```bash
known recall 'auth flow' --threshold 0.5
```

**Raise `--recency`** when you need the most recently observed knowledge:

```bash
known recall 'deployment config' --recency 0.4
```

**Use `--text`** for exact keyword or phrase matching (FTS5):

```bash
known recall --text 'rate-limit'
```

**Use `--provenance verified`** when you need only confirmed facts:

```bash
known recall 'API contract' --provenance verified
```

## Flag Reference

| Flag | Default | Purpose |
|------|---------|---------|
| `--scope` | auto (from cwd) | Scope to search within |
| `--threshold` | 0.4 | Minimum similarity (raise for precision, lower for recall) |
| `--recency` | 0.1 | Recency weight (0=pure similarity, 1=pure recency) |
| `--expand-depth` | 0 | Graph expansion hops from each result |
| `--text` | false | Use FTS5 full-text search instead of vector search |
| `--provenance` | all | Filter: `verified`, `inferred`, or `uncertain` |
| `--source` | all | Filter: `file`, `url`, `conversation`, or `manual` |
| `--label` | all | Filter by label (repeatable) |
| `--limit` | 5 | Maximum results |

## Scopes

Scope is auto-derived from your working directory. Override with `--scope`:

```bash
known recall 'query' --scope backend.api
```

Scope search is hierarchical: `backend` includes all `backend.*` descendants.

If the project has a `scope_prefix` in `.known.yaml`, `--scope` values are
automatically qualified (e.g., `--scope cmd` becomes `myproject.cmd`).
To access another project's knowledge, prefix with `/` to bypass qualification:

```bash
known recall 'auth tokens' --scope /otherproject.api
```
