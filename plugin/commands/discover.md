---
description: Walk a codebase and store curated architectural knowledge
argument-hint: [path] [--scope <prefix>] [--depth <shallow|standard|deep>]
---

Systematically analyze a codebase and store high-signal architectural knowledge
in the knowledge graph. This is **curated extraction**, not exhaustive indexing —
focus on facts that save future sessions from re-exploration.

## Arguments

- `path` (optional): Project root to analyze. Defaults to current working directory.
- `--scope <prefix>` (optional): Scope prefix for entries. Defaults to auto-derived
  from `.known.yaml` or directory name.
- `--depth <shallow|standard|deep>` (optional): How deep to analyze. Default: `standard`.
  - `shallow`: Top-level structure only (README, config, entry points)
  - `standard`: Key directories to 2-3 levels deep
  - `deep`: Every significant directory, function-level detail for core modules

## Instructions

### Phase 1: Orientation

1. Read project root docs (README, CLAUDE.md, build config, `.known.yaml`).

2. Determine the scope prefix:
   - If `.known.yaml` has `scope_prefix`, use that
   - If `--scope` was provided, use that
   - Otherwise derive from directory name

3. Check what knowledge already exists:
   ```bash
   known scope tree
   known recall --scope <prefix>
   ```
   - **First discovery**: focus on gaps — skip scopes that already have entries.
   - **Re-discovery** (entries already exist across most scopes): verify existing
     entries against current code. Update stale entries with `known update <id>`,
     supersede replaced ones with `--link supersedes:<old-id>`, and delete entries
     for code that no longer exists with `known delete <id> --force`.

### Phase 2: Scope Creation

Create scopes that mirror the project's logical structure. Not every directory
needs a scope — only those with distinct architectural purpose.

```bash
known scope create <prefix>.<module>
```

**Good scope granularity:**
- `myproject.api` — an API layer
- `myproject.storage` — persistence layer
- `myproject.auth` — authentication subsystem

**Too granular (avoid):**
- `myproject.api.handlers.users.create` — a single handler

### Phase 3: Directory Walk

For each significant directory, follow this process:

1. **Read key files** in priority order:
   - README or doc files in the directory
   - Entry point / main file (index.ts, main.go, __init__.py, mod.rs, etc.)
   - Type definitions / models / interfaces
   - Configuration files
   - Read enough files to understand purpose — usually 2-5 per directory

2. **Extract atomic facts** that capture:
   - What this module/directory does (purpose)
   - Key types, interfaces, or patterns it exposes
   - Important architectural decisions visible in the code
   - Non-obvious conventions

3. **Store via `known add`** (one at a time or in batch):
   ```bash
   # Single entry:
   known add '<atomic fact>' \
     --title '<2-5 word label>' \
     --scope <prefix>.<module> \
     --source-type file \
     --source-ref '<primary file analyzed>' \
     --provenance inferred
   ```

   For many entries at once, use `known add --batch` (much faster):
   ```bash
   cat <<'JSONL' | known add --batch
   {"content": "fact one", "title": "Label", "scope": "prefix.mod", "source_type": "file", "source_ref": "main.go"}
   {"content": "fact two", "title": "Other", "scope": "prefix.mod", "source_type": "file", "source_ref": "lib.go"}
   JSONL
   ```

4. **Skip generated/vendored directories:**
   `vendor/`, `node_modules/`, `dist/`, `build/`, `target/`, `__pycache__/`,
   `.git/`, `.cache/`, `.next/`, protobuf output, codegen, test fixtures.

### Phase 4: Synthesis

Before storing cross-cutting knowledge, review everything you've learned and
reason holistically:

- What is the overall architecture? (e.g., layered monolith, microservices,
  plugin system, pipeline)
- What patterns recur across modules? (error handling style, naming conventions,
  shared abstractions)
- Where are the key boundaries and interfaces between components?
- What would surprise a new contributor? What's non-obvious?
- Do any per-module entries need revision now that you see the full picture?

This step produces better cross-cutting entries because they're informed by the
complete walk, not incremental impressions.

### Phase 5: Cross-Cutting Knowledge

Store project-wide facts informed by your synthesis:

1. **Architecture overview** — one entry in the root scope summarizing the overall
   system design, key patterns, and technology choices.

2. **Build and deploy** — how to build, test, and deploy. CI/CD pipeline if present.

3. **Conventions** — naming patterns, error handling approach, test patterns,
   anything a new contributor would need to know.

4. **Key relationships** — use `--link` on `add` to create edges inline, or link
   after the fact with `known link`. Prefer inline linking when you have the target ID:
   ```bash
   known add '<detail>' --scope <prefix>.<module> --link elaborates:<overview-id> ...
   ```

### Quality Standards

**Each entry must be:**
- **Atomic**: One fact per entry. If you're writing "and" or listing multiple things,
  consider splitting.
- **Non-obvious**: Don't store what's evident from file names or standard framework
  conventions. Store what requires reading code to learn.
- **Actionable**: A future session should be able to use this fact to make decisions
  or skip exploration.
- **Sourced**: `--source-ref` must point to the actual file(s) analyzed, not a
  generic directory.

**Target density:**
- `shallow`: 3-5 entries total
- `standard`: 1-2 entries per significant directory, 10-25 entries total
- `deep`: 2-4 entries per directory, 30-60 entries total

### Phase 6: Summary

After storing knowledge, report:
- Number of scopes created
- Number of entries stored
- Number of edges created
- Run `known scope tree` to show the hierarchy
- Run `known stats` to show totals

## Example Output

For a typical Go web service:

```
Created 6 scopes, 14 entries, 8 edges.

myproject
  api
    handlers
    middleware
  storage
  model
  config

Entries: 14  Edges: 8  Scopes: 6
```
