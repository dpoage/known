# /known-search — Search entries with full control

Search the knowledge graph with structured output, similarity scores, and
optional JSON. Use this when you need entry IDs or machine-readable results.

## Usage

```
/known-search <query> [flags]
```

For general knowledge retrieval, use `/recall` instead — it returns
plain text optimized for LLM context.

## When to Use Search

- **Need entry IDs** to update, delete, link, or inspect entries
- **Need JSON output** for programmatic processing (`known --json search`)
- **Need similarity scores** to evaluate result quality
- **Investigating graph structure** with `--hybrid` expansion

## Instructions

1. Run the search command:

```bash
known search '<query>' [flags]
```

2. Present the results with their IDs and similarity scores.

3. Suggest follow-up actions based on the results:
   - `/forget` to delete an unwanted entry
   - `known show <id>` to see full details and relationships
   - `known update <id> --content '...'` to correct an entry

## Flag Reference

| Flag | Default | Purpose |
|------|---------|---------|
| `--scope` | auto | Scope to search within |
| `--limit` | 10 | Maximum results |
| `--threshold` | 0.3 | Raise to get fewer, more relevant results |
| `--recency` | 0 | Raise to prefer recently observed knowledge |
| `--hybrid` | false | Follow graph edges to find related entries |
| `--expand-depth` | 1 | Graph expansion hops (with `--hybrid`) |

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
```
