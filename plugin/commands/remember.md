---
description: Store a fact in the knowledge graph
argument-hint: <fact to store>
---

Extract a single atomic fact from the user's message and store it.

## Instructions

1. Extract the core fact from the user's input. Rewrite it as a single, clear sentence if needed. Do not store multi-sentence paragraphs — break them into separate calls.

2. Choose the appropriate flags:
   - `--source-type conversation` (always, since this comes from chat)
   - `--source-ref claude-code`
   - `--confidence`: Use `verified` if the user stated it as fact or confirmed it. Use `inferred` if you're deriving it from context. Use `uncertain` if it might be wrong.
   - `--scope`: Omit to use the auto-derived scope, or set explicitly if the fact belongs elsewhere.
   - `--ttl`: Omit for the default. Set explicitly for temporary facts (e.g., `168h` for a 1-week workaround).

3. Run the command:

```bash
known add '<atomic fact>' --source-type conversation --source-ref claude-code --confidence <level>
```

4. Report the stored entry ID back to the user.

## Examples

User says: "We decided to use ULIDs instead of UUIDs for all new tables"

```bash
known add 'All new database tables use ULIDs instead of UUIDs' --source-type conversation --source-ref claude-code --confidence verified
```

User says: "The staging API might be at api.staging.example.com but I'm not sure"

```bash
known add 'Staging API endpoint may be api.staging.example.com' --source-type conversation --source-ref claude-code --confidence uncertain --ttl 168h
```
