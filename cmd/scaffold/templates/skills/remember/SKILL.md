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
Link?   related-to:01KXSBBCBHP8NB6ZCETENBBSX8 "Schema conventions"
Link?   related-to:01KXSBBCPN8BM3Q0ESXH57RE6Z "Migration tooling"
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
          known add '<correction>' --supersedes 'All new database tables use ULIDs instead of integers'
```

The fact is already stored. Use the hints to extend or correct it.

## Optional enrichment flags

All flags are optional. Defaults are applied automatically.

| Flag | Default | When to add it |
|------|---------|----------------|
| `--scope <path>` | auto from cwd | Storing to a specific module scope |
| `--provenance <level>` | `inferred` | Use `verified` when the user stated the fact directly |
| `--source-type <type>` | `manual` | Use `file` when derived from a source file |
| `--source-ref <ref>` | `cli` | Path to the source (with `--source-type file`) |
| `--ttl <duration>` | permanent (no expiry) | Opt-in for ephemera: `--ttl 24h`, `--ttl 168h`; `--ttl 0` is a no-op (explicit permanent) |
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

## One-shot correction (supersede)

When a fact has changed, store the replacement and link it to the old entry in
one command — no ULIDs required:

```bash
known add 'corrected fact' --supersedes 'old fact content'
```

The `--supersedes` argument is a content query resolved by exact-match or semantic
dominance. Ambiguous query? The command aborts before writing anything.

On success:

```
Stored  01KYABC...
Scope   myproject
        "corrected fact"
Supersedes 01KYABC... -[supersedes]-> 01KXABC...
```

## Batch mode

For many facts at once (single embedding pass, much faster):

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
