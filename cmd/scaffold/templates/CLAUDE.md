# Known — LLM Memory Graph

This project uses [known](https://github.com/dpoage/known) for persistent knowledge storage.
Knowledge persists across sessions — use it to avoid re-learning the same things.

**Core rule:** When you see knowledge scopes in your context, use `/recall` before
exploring the codebase. Recall is cheaper than re-reading source files.

```bash
known recall 'API conventions' --scope backend.api
```

Scopes are auto-derived from your working directory. Override with `--scope`.

## Skills

| Skill | Purpose |
|-------|---------|
| `/remember` | Store a fact from the conversation |
| `/recall` | Retrieve knowledge relevant to a query |
| `/forget` | Find and delete an entry |
| `/known-search` | Search with full control over flags |

## Labels

Labels let you tag and filter knowledge entries. Use `--label` (repeatable) on
`add`, `update`, `list`, `search`, and `recall`:

```bash
known add 'Rate limit is 100 req/s' --label rate-limiting --label backend
known recall 'rate limits' --label backend
known list --label rate-limiting
known label list   # enumerate all labels in the graph
```

Labels use AND semantics: `--label a --label b` returns entries with both labels.
