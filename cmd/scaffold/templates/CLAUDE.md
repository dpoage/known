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
