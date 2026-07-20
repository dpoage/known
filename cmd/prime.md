# Known — Persistent Memory Graph for LLMs

Store knowledge across sessions so agents avoid re-learning the same things.

## When to store

Store a fact the moment you learn it — decisions, conventions, environment
details, user preferences, non-obvious architecture. The biggest reason agents
get no value from known is never invoking it. Don't ask permission; store and
tell the user what you remembered.

## Commands

| Command | Purpose |
|---------|---------|
| `known add <fact>` | Store a fact (alias: `known remember`) |
| `known recall '<query>'` | Retrieve knowledge — hybrid vector + text + graph search |
| `known recall --scope <path>` | List all entries in a scope |
| `known forget '<content or ULID>' --force` | Delete an entry (alias: `known delete`) |
| `known search '<query>'` | Scored results with IDs (`known --json search` for JSON) |
| `known show <id>` | Full entry details with relationships |
| `known scope tree` | Show the scope hierarchy |
| `known stats` | Entry, edge, and scope counts |

## Capture — one-shot, no ceremony

```bash
known add <fact as plain words>
```

No quotes, no required flags. Scope is auto-derived from your working directory.
No `known init` or `.known.yaml` needed.

Confirmation block:
```
Stored  01KXSBAZ7HZ71JFM5FGHRHVKMX
Scope   myproject
        "All new database tables use ULIDs instead of integers"
Link?   related-to:01KXSBBCBHP8NB6ZCETENBBSX8 "Schema conventions"
```

IDs are ULIDs (26 alphanumeric chars), never integers.

**Dedup is success:** if content already exists you'll see `Duplicate <ULID>` with
hints on how to extend or correct it (`known update`, `--link elaborates:`, or
`known add '<correction>' --supersedes '<old content>'`). Not a failure — the fact is already stored.

**Link suggestions** (`Link?` lines): accept with `known link accept '<content>' --all`
or selectively: `known link accept '<content>' 1 2`.

**One-shot correction**: when a fact changes, store the replacement and link it
to the old entry in a single command — no ULIDs typed:

```bash
known add 'corrected fact' --supersedes 'old fact content'
```

The `--supersedes` query resolves by content (exact-match or semantic dominance).
Ambiguous query? The command aborts before writing anything — refine the query or
use a ULID directly.

## Behavior

- **Recall before exploring**: check for stored knowledge before reading source
  files. If no results, proceed normally.
- **Store proactively**: facts learned mid-task should be stored immediately, not
  after the task completes. One entry per atomic fact.
- **Don't duplicate**: skip ephemeral state, info already in code/docs, and speculation.
- **Re-prime anytime**: `known prime` reprints CLI guidance with live graph status.
- **If `known` is unavailable** (commands fail): ignore these instructions and
  proceed normally.
