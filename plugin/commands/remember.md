---
description: Store a fact in the knowledge graph
argument-hint: <fact or context to store>
---

Extract a single atomic fact and store it in the knowledge graph.
You don't need to ask permission — just store it and tell the user what you remembered.

## Instructions

1. Extract the core fact. Rewrite as a single, clear sentence if needed.
   For multiple facts, make separate calls.

2. Run the command:

```bash
known add '<atomic fact>' --title '<2-5 word label>' --source-type conversation --source-ref claude-code --provenance <level>
```

3. Report the stored entry ID back to the user.

## Flags

| Flag | Required | Purpose |
|------|----------|---------|
| `--title` | Yes | Short label (2-5 words) for browsing |
| `--source-type` | Yes | `conversation` (from chat) or `file` (from exploration) |
| `--source-ref` | Yes | Origin reference (use `claude-code` for session facts) |
| `--provenance` | Yes | `verified` (user stated), `inferred` (derived), or `uncertain` |
| `--scope` | No | Target scope (auto-derived if omitted) |
| `--ttl` | No | Expiry duration (e.g., `168h` for 1 week). Omit for permanent. |
| `--link` | No | Link to existing entry: `--link type:target-id`. Repeatable. Types: `elaborates`, `depends-on`, `related-to`, `contradicts`, `supersedes` |

## Scope Qualification

`--scope` values are relative to the project's `scope_prefix` (from `.known.yaml`).
Do NOT include the prefix yourself — it is added automatically.

Example: if `scope_prefix` is `myproject`, then `--scope cmd` stores to `myproject.cmd`.

To store in another project's scope, prefix with `/` to bypass qualification:
`--scope /otherproject.api`.

## Examples

User states a decision:
```bash
known add 'All new database tables use ULIDs instead of UUIDs' --title 'ULID over UUID' --source-type conversation --source-ref claude-code --provenance verified
```

Uncertain fact with TTL:
```bash
known add 'Staging API endpoint may be api.staging.example.com' --title 'Staging API endpoint' --source-type conversation --source-ref claude-code --provenance uncertain --ttl 168h
```

Finding from codebase exploration:
```bash
known add 'All API route definitions live in cmd/api/routes.go using a central router' --title 'API route location' --source-type file --source-ref cmd/api/routes.go --provenance inferred --scope backend.api
```

Linking to an existing entry (ID from recall output):
```bash
known add 'The central router uses chi with middleware for auth, logging, and rate limiting' --title 'Router middleware stack' --source-type file --source-ref cmd/api/routes.go --provenance inferred --scope backend.api --link elaborates:01ABC123DEF456GHJ789KLMNOP
```

## Batch Mode

For many entries at once, use `known add --batch` (much faster):
```bash
cat <<'JSONL' | known add --batch
{"content": "fact one", "title": "Label", "scope": "prefix.mod", "source_type": "file", "source_ref": "main.go"}
{"content": "fact two", "title": "Other", "scope": "prefix.mod", "source_type": "file", "source_ref": "lib.go"}
JSONL
```
