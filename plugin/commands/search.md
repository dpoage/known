---
description: Search the knowledge graph with full control over flags
argument-hint: <query> [flags]
---

Search the knowledge graph with structured output and similarity scores.
Use this when you need entry IDs, JSON output, or score-based evaluation.

For general retrieval, use `/known:recall` instead — it uses hybrid search
by default and returns plain text optimized for LLM context.

## When to Use

- **Need entry IDs** to update, delete, link, or inspect entries
- **Need JSON output** for programmatic processing (`known --json search`)
- **Need similarity scores** to evaluate result quality
- **Pure vector search** without the hybrid text+graph expansion that recall adds

## Instructions

1. Run the search command:

```bash
known search '<query>' [flags]
```

2. Present the results with their IDs and similarity scores.

3. Suggest follow-up actions based on the results:
   - `/known:forget` to delete an unwanted entry
   - `known show <id>` to see full details and relationships
   - `known update <id> --content '...'` to correct an entry

## Flag Reference

| Flag | Default | Purpose |
|------|---------|---------|
| `--scope` | auto (from cwd) | Scope to search within |
| `--limit` | 5 | Maximum results |
| `--threshold` | 0.4 | Minimum similarity (raise for precision) |
| `--recency` | 0 | Recency weight (0=pure similarity, 1=pure recency) |
| `--text` | false | Use FTS5 full-text search instead of vector search |
| `--hybrid` | false | Enable vector + graph expansion (recall does this by default) |
| `--expand-depth` | 1 | Graph expansion hops (only with `--hybrid`) |
| `--label` | all | Filter by label (repeatable) |

Global flags go **before** the subcommand:

```bash
known --json search '<query>'     # JSON output
known --quiet search '<query>'    # suppress non-essential output
```

## Examples

```bash
known search 'deployment process' --limit 5
known search 'API rate limits' --hybrid --expand-depth 2
known --json search 'database schema' --scope backend
known search 'auth' --threshold 0.5 --recency 0.3
known search --text 'rate-limit'
```
