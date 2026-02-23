---
description: Store a fact in the knowledge graph
argument-hint: <fact or context to store>
---

Extract a single atomic fact and store it in the knowledge graph.

## When to Store

Use `/known:remember` proactively when:
- The user makes a decision ("we chose Postgres over SQLite because...")
- The user states an environment fact ("staging DB is at 10.0.1.5")
- The user corrects a misconception ("the API returns 201, not 200")
- The user expresses a preference ("always use tabs, not spaces")
- The user shares tribal knowledge not captured in code or docs

Also store **your own findings from codebase exploration** — architecture patterns,
key file locations, non-obvious conventions, important config values. These save
future sessions from re-reading the same files. Use `--source-type file` and
`--provenance inferred` for these.

You don't need to ask permission — just store it and tell the user what you remembered.

## What NOT to Store

- Ephemeral context (current task steps, debugging state)
- Information already in code or docs (don't duplicate the repo)
- Speculative or unverified conclusions
- Obvious facts derivable from the codebase

## Instructions

1. Extract the core fact from the user's input. Rewrite it as a single, clear sentence if needed. Do not store multi-sentence paragraphs — break them into separate `/known:remember` calls.

2. Choose the appropriate flags:
   - `--title`: Include a short title (2-5 words) that captures what the fact is about. This makes entries much easier to browse in `list` output.
   - `--source-type conversation` for facts from chat, or `--source-type file` for findings from codebase exploration
   - `--source-ref claude-code`
   - `--provenance`: Use `verified` if the user stated it as fact or confirmed it. Use `inferred` if you're deriving it from context. Use `uncertain` if it might be wrong.
   - `--scope`: Use specific, granular scopes rather than broad ones. For example, `--scope model.architecture` rather than `--scope model`, or `--scope storage.sqlite` rather than `--scope storage`. This keeps knowledge organized and recall precise. If the project has a `scope_prefix` in `.known.yaml`, `--scope` values are automatically qualified with the prefix (e.g., `--scope cmd` becomes `myproject.cmd`). To bypass qualification for cross-project scopes, prefix with `/` (e.g., `--scope /otherproject.api`).
   - `--ttl`: Omit for the default. Set explicitly for temporary facts (e.g., `168h` for a 1-week workaround).
   - `--link`: Link the new entry to an existing entry. Format: `--link type:target-id`. Repeatable for multiple links. Common types: `elaborates`, `depends-on`, `related-to`, `contradicts`, `supersedes`. Use entry IDs from `recall` output (shown in `{curly braces}`).

3. Run the command:

```bash
known add '<atomic fact>' --title '<short label>' --source-type conversation --source-ref claude-code --provenance <level>
```

4. Report the stored entry ID back to the user.

## Examples

User says: "We decided to use ULIDs instead of UUIDs for all new tables"

```bash
known add 'All new database tables use ULIDs instead of UUIDs' --title 'ULID over UUID' --source-type conversation --source-ref claude-code --provenance verified
```

User says: "The staging API might be at api.staging.example.com but I'm not sure"

```bash
known add 'Staging API endpoint may be api.staging.example.com' --title 'Staging API endpoint' --source-type conversation --source-ref claude-code --provenance uncertain --ttl 168h
```

You discover during exploration that all API routes are defined in `cmd/api/routes.go`:

```bash
known add 'All API route definitions live in cmd/api/routes.go using a central router' --title 'API route location' --source-type file --source-ref claude-code --provenance inferred --scope backend.api
```

You want to store a detail that elaborates on an existing entry (ID from recall output):

```bash
known add 'The central router uses chi with middleware for auth, logging, and rate limiting' --title 'Router middleware stack' --source-type file --source-ref claude-code --provenance inferred --scope backend.api --link elaborates:01ABC123DEF456GHJ789KLMNOP
```
