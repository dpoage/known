# Known — Persistent Memory Graph for LLMs

Store knowledge across sessions so agents avoid re-learning the same things.

## Skills

| Skill | Purpose |
|-------|---------|
| `/remember` | Store a fact from the conversation |
| `/recall` | Retrieve knowledge relevant to a query |
| `/forget` | Find and delete an entry |
| `/known-search` | Search with full control over flags |
| `/discover` | Walk a codebase and store architectural knowledge |

## Behavior

- **Recall before exploring**: If a scope tree appeared at session start,
  check for stored knowledge before reading source files. If recall returns
  no results, proceed normally — the graph may not have entries yet.
- **Store proactively**: When the user states decisions, preferences, environment
  facts, or corrections, store them without asking. Tell the user what you stored.
- **Atomic facts**: One fact per entry. No multi-sentence paragraphs.
- **Don't duplicate**: Skip ephemeral state, info already in code/docs, and speculation.
- **If `known` is unavailable** (no scope tree, commands fail): ignore these
  instructions and proceed normally.
