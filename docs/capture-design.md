# Capture Surface Design — zv1.2

**Bead:** known-zv1.2  
**Date:** 2026-07-17  
**Ground truth:** docs/friction-audit.md, docs/IDENTITY.md

---

## Problem statement

93% of sessions store nothing. When agents do try to store, three failure modes
dominate: (2) output buries the result under embedding boilerplate so the agent
cannot confirm what was stored; (3) bare `known remember` returns no output at
all; (4) agents grep for integer IDs in --json output and get nothing because
IDs are ULIDs. A fourth failure mode (6) is flag ceremony: agents invent flags
like `--confidence` that don't exist, get a bare error, and retry without them.
The omp plugin is dead because discover.md YAML frontmatter uses unquoted `[`
which the YAML parser treats as a sequence literal.

IDENTITY principle 1: one-shot capture, no required ceremony, errors state the
fix, output suggests the next action.

---

## Failure mode before/after

### Mode 2 — Output buries result

**Before:**
```
$ known remember "DeferredRenderer uses distance fields" --scope engine.graphics 2>&1 | tail -2
Expires:    2026-09-03T19:07:26Z
Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)
```
Agent sees embedding model name. Does not know the ULID, scope, or content echo.

**After:**
```
$ known add DeferredRenderer uses distance fields --scope engine.graphics
Stored  01KTD44NBEHMHE0MPY6VGFCJR6
Scope   engine.graphics
        "DeferredRenderer uses distance fields"
Link?   elaborates:01KSD33MAAGKG0LPY5UFCJR5 "Renderer architecture"
        related-to:01KRC22LZZFJF9KOX4TDBIE4 "Distance field SDF overview"
```
Embedding init noise goes to stderr. stdout is always the confirmation block.

### Mode 3 — Silent success

**Before:**
```
$ known remember "Renderer architecture decision..."
(Bash completed with no output)
```

**After:**
```
$ known add Renderer architecture decision
Stored  01KTD44NBEHMHE0MPY6VGFCJR6
Scope   root
        "Renderer architecture decision"
```
Every successful store prints at minimum the three-line confirmation block.

### Mode 4 — ID format mismatch

**Before** (agent tries to link a correction to the original):
```sh
ID=$(known --json search 'renderer architecture' 2>/dev/null \
    | grep -o '"id":[0-9]*' | head -1 | grep -o '[0-9]*')
# ID is empty — ULIDs contain letters, [0-9]* matches nothing
known remember "CORRECTION: ..."
```
Graph is broken: two disconnected entries, no supersedes edge.

**After** — --json search emits a stable object where `id` is clearly a ULID
string; the output design doc and skill both state IDs are ULIDs (alphanumeric,
26 chars). The add confirmation includes link suggestions, so the agent sees
the related entry ID immediately after storing and can link without a separate
search:
```
Stored  01KUE55OCCINIC1NQY7WHDLEJR7
Scope   engine.graphics
        "CORRECTION to renderer decoupling decision..."
Link?   supersedes:01KTD44NBEHMHE0MPY6VGFCJR6 "Renderer architecture decision"
```

### Mode 5/6 — Unknown flag / flag ceremony

**Before:**
```
$ known remember "Three feature epics..." --confidence verified --source conversation
error: unknown flag: --confidence
```
Agent retries without flags (burns a round trip, loses provenance intent).

**After:**
```
$ known add "Three feature epics..." --confidence verified
error: unknown flag: --confidence
       Did you mean: --provenance?
       Usage: known add <content> [flags]
              known add --help for full flag list
```
Nearest-valid-flag suggestion by edit distance from the known flag set. Agent
fixes on the first retry.

### Mode 7 — omp plugin YAML parse failure (discover.md)

**Before** (`argument-hint` value starts with `[`, parsed as YAML sequence):
```yaml
---
description: Walk a codebase and store curated architectural knowledge
argument-hint: [path] [--scope <prefix>] [--depth <shallow|standard|deep>]
---
```
omp logs: `YAML Parse error: Unexpected token` — plugin is silently dead.

**After** (value quoted):
```yaml
---
description: Walk a codebase and store curated architectural knowledge
argument-hint: "[path] [--scope <prefix>] [--depth <shallow|standard|deep>]"
---
```

---

## Input parsing design

### Multi-word content without quotes

Remaining positional args after flag parsing are joined with spaces:

```
known add some fact without quotes
# → content = "some fact without quotes"

known add "quoted fact"
# → content = "quoted fact"
```

This matches how agents naturally invoke commands from skill instructions.
`pflag` stops flag scanning at `--`; args before `--` that aren't flags
become positional args which we join.

### Stdin

When the first (and only) positional arg is `-`, content is read from stdin:

```
echo "multi-line\nfact" | known add -
known add - <<'EOF'
A long fact that spans
multiple lines.
EOF
```

### Content length

Max 4096 bytes (model.MaxContentLength). Error: `content exceeds 4096 bytes;
split into smaller entries`.

### Forgiving provenance

`--provenance` accepts: `verified`, `inferred`, `uncertain` (existing).
When `--confidence` is passed, the error message suggests `--provenance`.

---

## Output design

### Human mode (default)

```
Stored  <ULID>           ← or "Duplicate <ULID>" if deduped
Scope   <scope>
        "<content truncated to 120 chars>"
Link?   <edge-type>:<ULID> "<title>"    ← 0–3 suggestions from zv1.3
        <edge-type>:<ULID> "<title>"
```

Rules:
- Always printed to stdout (never suppressed).
- Embedding model init prints to stderr or is already silent.
- `Link?` section omitted when no suggestions.
- On dedup hit, line 1 is `Duplicate <existing-ULID>` and content echo shows
  the existing entry's content.

### Machine mode (--json)

```json
{
  "id": "01KTD44NBEHMHE0MPY6VGFCJR6",
  "scope": "engine.graphics",
  "content": "DeferredRenderer uses distance fields",
  "deduped": false,
  "suggestions": [
    {
      "id": "01KSD33MAAGKG0LPY5UFCJR5",
      "title": "Renderer architecture",
      "edge_type": "elaborates",
      "score": 0.91
    }
  ]
}
```

Notes:
- `id` is always a ULID string (26 alphanumeric chars, e.g. `01KTD44NBEHMHE0MPY6VGFCJR6`).
  Agents MUST NOT grep for `"id":[0-9]+` — ULIDs contain letters.
- `deduped: true` means the content already existed; `id` is the existing entry's ID.
- `suggestions` is always present (empty array if none).

### Dedup presentation

When `CreateOrUpdate` returns `Version > 1` (or a future `ErrDuplicateContent`),
treat as a useful outcome, not an error:

```
Duplicate 01KTD44NBEHMHE0MPY6VGFCJR6
Scope     engine.graphics
          "DeferredRenderer uses distance fields"
Hint      known update 01KTD44NBEHMHE0MPY6VGFCJR6 --content '...'
          known add '<new fact>' --link elaborates:01KTD44NBEHMHE0MPY6VGFCJR6
```

---

## Flag unknown-flag error design

On `pflag.ContinueOnError` parse failure for an unknown flag:

1. Extract the unknown flag name from the error string.
2. Compute Levenshtein distance to each valid flag name.
3. If best distance ≤ 3, suggest it.
4. Print:
   ```
   error: unknown flag: --<name>
          Did you mean: --<suggestion>?
          Usage: known add <content> [flags]
                 known add --help for full flag list
   ```

Valid flag set for `known add`: title, scope, source-type, source-ref,
provenance, observed-by, source-hash, ttl, meta, label, link, batch.

---

## zv1.3 interface contract

```go
// package query

type LinkSuggestion struct {
    Entry    model.Entry
    Score    float64
    EdgeType model.EdgeType
}

func (e *Engine) SuggestLinks(ctx context.Context, entry model.Entry, k int) ([]LinkSuggestion, error)
```

Call site in `cmd/add.go` (single call, after successful store):

```go
if app.Engine != nil {
    suggestions, err := app.Engine.SuggestLinks(ctx, *result, 3)
    if err != nil {
        fmt.Fprintf(app.Stderr, "warning: link suggestions: %v\n", err)
    }
    // pass to printer
}
```

Error is non-fatal: warnings go to stderr, output proceeds without suggestions.

Until zv1.3 commits `query/suggest.go`, a stub with the same signature lives
in `query/suggest_stub.go` (build-tag-guarded or simply returning nil,nil)
so this branch compiles independently.

---

## Files changed

| File | Change |
|------|--------|
| `cmd/add.go` | Multi-word join, stdin `-`, unknown-flag suggestion, dedup output, suggestion call site |
| `cmd/output.go` | `PrintAddResult` (human+JSON), move embedding noise to stderr |
| `cmd/add_test.go` | Table tests: input parsing, output contract, error suggestion, dedup |
| `plugin/commands/discover.md` | Quote `argument-hint` value |
| `query/suggest_stub.go` | Compile stub until zv1.3 lands (removed after merge) |
| `README.md` | Fix `--confidence` → `--provenance`; update capture examples |
