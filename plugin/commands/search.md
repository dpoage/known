---
description: Search the knowledge graph with full control over flags
argument-hint: <query> [flags]
---

Search the knowledge graph with all available flags exposed.

Use this instead of `/known:recall` when you need entry IDs (for show, update, or
delete), want JSON output, or need fine-grained control over similarity thresholds
and search strategy.

## Instructions

1. Run the search command with the user's query and any flags they specified:

```bash
known search '<query>' [flags]
```

### Available flags

| Flag | Default | Description |
|------|---------|-------------|
| `--scope` | auto | Scope to search within |
| `--limit` | 10 | Maximum results |
| `--threshold` | 0.3 | Minimum similarity (0-1) |
| `--recency` | 0 | Recency weight (0=similarity only, 1=recency only) |
| `--hybrid` | false | Use hybrid vector+graph search |
| `--expand-depth` | 1 | Graph expansion depth (with --hybrid) |

Note: `--json` and `--quiet` are global flags — they go **before** the subcommand:
```bash
known --json search '<query>'
```

2. Present the results with their IDs and similarity scores.

3. Suggest follow-up actions based on the results:
   - `/known:forget` to delete an unwanted entry
   - `known show <id>` to see full details and relationships
   - `known update <id> --content '...'` to correct an entry
   - Broaden with lower `--threshold` or narrow with higher value

## When to Use Search vs Recall

- **Need entry IDs** (to update, delete, link, or show details) → search
- **Need JSON output** (programmatic processing) → search with `--json`
- **Tuning results** (threshold, limit, recency weighting) → search
- **Graph-expanded results** (follow relationships) → search with `--hybrid`
- **Just need context for your work** → use `/known:recall` instead

## Examples

```bash
known search 'deployment process' --limit 5
known search 'API rate limits' --hybrid --expand-depth 2
known --json search 'database schema' --scope backend
known search 'auth' --threshold 0.5 --recency 0.3
```
