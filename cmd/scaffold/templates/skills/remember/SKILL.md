# /remember — Store a fact in known

Extract a single atomic fact and store it immediately — no flags required.
Do not ask permission. Store it and tell the user what you remembered.

## Usage

```
/remember <fact to store>
```

## Primary form

```bash
known add <fact as plain words>
```

No quotes needed. Multi-word content is captured exactly as typed.

Example output:

```
Stored  01KXSBAZ7HZ71JFM5FGHRHVKMX
Scope   myproject
        "All new database tables use ULIDs instead of integers"
Link?   elaborates:01KXSBBCBHP8NB6ZCETENBBSX8 "Schema conventions"
        related-to:01KXSBBCPN8BM3Q0ESXH57RE6Z "Migration tooling"
```

- `Stored` + ULID confirms success. IDs are ULIDs (26 alphanumeric chars), never integers.
- `Scope` is auto-derived from your working directory — no setup required.
- `Link?` lines are suggestions. Accept with `known link accept '<content>' --all`
  or selectively: `known link accept '<content>' 1 2`.

## Dedup is success, not failure

If the content already exists:

```
Duplicate 01KXSBAZ7HZ71JFM5FGHRHVKMX
Scope     myproject
          "All new database tables use ULIDs instead of integers"
Hint      known update 01KXSBAZ7HZ71JFM5FGHRHVKMX --content '...'
          known add '<new fact>' --link elaborates:01KXSBAZ7HZ71JFM5FGHRHVKMX
```

The fact is already stored. Use the hints to extend or correct it.

## Optional enrichment flags

All flags are optional. Defaults: `provenance: inferred`, `source-type: manual`.

| Flag | Default | When to add it |
|------|---------|----------------|
| `--scope <path>` | auto from cwd | Storing to a specific module scope |
| `--provenance <level>` | `inferred` | Use `verified` when the user stated the fact directly |
| `--source-type <type>` | `manual` | Use `file` when derived from a source file |
| `--source-ref <ref>` | `cli` | Path to the source (with `--source-type file`) |
| `--ttl <duration>` | permanent | Expiry for time-limited facts (e.g. `168h`) |
| `--label <tag>` | none | Categorical tag, repeatable |
| `--link <type:id>` | none | Inline edge using a ULID from prior output |

When the user states a decision directly:

```bash
known add All new database tables use ULIDs --provenance verified
```

When deriving a fact from a file:

```bash
known add API routes defined in cmd/api/routes.go --source-type file --source-ref cmd/api/routes.go
```

## Batch mode

For many facts at once (single embedding pass, much faster):

```bash
cat <<'JSONL' | known add --batch
{"content": "fact one about auth", "scope": "myproject.auth"}
{"content": "fact two about storage"}
JSONL
```
