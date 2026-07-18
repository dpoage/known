---
description: Store a fact in the knowledge graph
argument-hint: <fact to store>
---

Extract a single atomic fact and store it immediately — no flags required.
Do not ask permission. Store it and tell the user what you remembered.

## Primary form

```bash
known add <fact as plain words>
```

No quotes needed. Multi-word content is captured exactly as typed.

Example output after a successful store:

```
Stored  01KXSBAZ7HZ71JFM5FGHRHVKMX
Scope   myproject
        "All new database tables use ULIDs instead of integers"
Link?   related-to:01KXSBBCBHP8NB6ZCETENBBSX8 "Schema conventions"
Link?   related-to:01KXSBBCPN8BM3Q0ESXH57RE6Z "Migration tooling"
```

- `Stored` + ULID confirms success. IDs are ULIDs (26 alphanumeric chars), never integers.
- `Scope` is auto-derived from your working directory — no setup required.
- `Link?` lines are suggestions from the existing graph. Use `known link accept` to accept them (see Linking section).

## Dedup is success, not failure

If the content already exists, you'll see:

```
Duplicate 01KXSBAZ7HZ71JFM5FGHRHVKMX
Scope     myproject
          "All new database tables use ULIDs instead of integers"
Hint      known update 01KXSBAZ7HZ71JFM5FGHRHVKMX --content '...'
          known add '<new fact>' --link elaborates:01KXSBAZ7HZ71JFM5FGHRHVKMX
Link?     related-to:01KXSBBCPN8BM3Q0ESXH57RE6Z "Migration tooling"
```

This is correct behavior. The fact is already stored. Use the hints to extend or correct it.

## Optional enrichment flags

All flags are optional. Defaults are applied automatically.

| Flag | Default | When to add it |
|------|---------|----------------|
| `--scope <path>` | auto from cwd | Storing to a specific module scope |
| `--provenance <level>` | `inferred` | Use `verified` when the user stated the fact directly |
| `--source-type <type>` | `manual` | Use `file` when derived from a specific source file |
| `--source-ref <ref>` | `cli` | Path or reference to the source (with `--source-type file`) |
| `--ttl <duration>` | 90d (`manual`), 7d (`conversation`) | Pass `--ttl 0` for a fact that must never expire |
| `--label <tag>` | none | Categorical tag, repeatable |
| `--link <type:id>` | none | Inline edge: `--link related-to:01KJ...` (ULID, not integer) |

When the user states a decision directly, add `--provenance verified`:

```bash
known add All new database tables use ULIDs --provenance verified
```

When deriving a fact from a file you read:

```bash
known add API route definitions live in cmd/api/routes.go --source-type file --source-ref cmd/api/routes.go
```

## Accepting link suggestions

After `known add`, the `Link?` output shows suggestions. To accept them:

```bash
known link accept '<stored content>' --all
known link accept '<stored content>' 1 2
```

Or link two entries directly by content:

```bash
known link "<text a>" "<text b>" --type related-to
```

## Batch mode

For many facts at once (much faster — single embedding pass):

```bash
cat <<'JSONL' | known add --batch
{"content": "fact one about auth", "scope": "myproject.auth"}
{"content": "fact two about storage"}
JSONL
```

Batch output:

```
Embedding 2 entries...
Writing 2 entries...
Batch complete: 2 created, 0 updated, 0 failed.
```
