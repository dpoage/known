# /discover — Walk a codebase and store architectural knowledge

Systematically analyze a codebase and store high-signal architectural knowledge
in the knowledge graph. This is **curated extraction**, not exhaustive indexing —
focus on facts that save future sessions from re-exploration.

## Usage

```
/discover [path] [--scope <prefix>] [--depth <shallow|standard|deep>]
```

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

1. Read the project root to understand what exists:
   - `ls` the root directory
   - Read `README.md`, `CLAUDE.md`, or equivalent docs
   - Read build config (`package.json`, `go.mod`, `Cargo.toml`, `CMakeLists.txt`, `pyproject.toml`, etc.)
   - Read `.known.yaml` if it exists to get the scope prefix

2. Determine the scope prefix:
   - If `.known.yaml` has `scope_prefix`, use that
   - If `--scope` was provided, use that
   - Otherwise derive from directory name

3. Check what knowledge already exists:
   ```bash
   known scope tree
   known recall --scope <prefix>
   ```
   Skip directories that already have good coverage. Focus on gaps.

### Phase 2: Scope Creation

Create scopes that mirror the project's logical structure. Not every directory
needs a scope — only those with distinct architectural purpose.

```bash
known scope create <prefix>.<module> "<description>"
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
   - Only read 2-5 files per directory — enough to understand purpose, not everything

2. **Extract one atomic fact** that captures:
   - What this module/directory does (purpose)
   - Key types, interfaces, or patterns it exposes
   - Important architectural decisions visible in the code
   - Non-obvious conventions

3. **Store via `known add`**:
   ```bash
   known add '<atomic fact>' \
     --title '<2-5 word label>' \
     --scope <prefix>.<module> \
     --source-type file \
     --source-ref '<primary file analyzed>' \
     --provenance inferred
   ```

4. **Skip these directories entirely:**
   - `vendor/`, `node_modules/`, `.vendor/`, `third_party/`
   - `dist/`, `build/`, `target/`, `__pycache__/`
   - `.git/`, `.cache/`, `.next/`
   - Generated code directories (protobuf output, codegen, etc.)
   - Test fixtures and test data directories

### Phase 4: Cross-Cutting Knowledge

After the directory walk, store project-wide facts:

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

### Phase 5: Summary

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

/myproject
  api
    handlers
    middleware
  storage
  model
  config

Entries: 14  Edges: 8  Scopes: 6
```
