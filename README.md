# known

A persistent memory graph for LLMs. Store facts during one session, recall them in the next.

LLM agents lose context between sessions. Decisions, environment details, codebase
conventions, user preferences — all gone after compaction. **known** is a local
knowledge graph that persists across sessions, so agents stop re-learning the same things.

Project identity, design principles, non-goals, and current goals live in
[docs/IDENTITY.md](docs/IDENTITY.md).

## How it works

```
Session 1: user says "we use ULIDs, not UUIDs"
  → known add 'All new tables use ULIDs instead of UUIDs' --source-type conversation

Session 2: agent is about to generate a migration
  → known recall 'ID generation strategy'
  → gets: "All new tables use ULIDs instead of UUIDs" [verified, root scope]
  → generates the migration correctly without asking
```

Entries are stored with vector embeddings for semantic search, organized into
hierarchical scopes, and connected by typed edges (depends-on, contradicts,
supersedes, elaborates, related-to).

## Install

Requires Go 1.25+.

```bash
go install github.com/dpoage/known/cmd/known@latest
```

Or build from source:

```bash
git clone https://github.com/dpoage/known.git
cd known
go build -o known ./cmd/known/
```

No CGo required. The default embedder (hugot) and database (SQLite) are both pure Go.

## Quick start

No setup required. `known add`, `known recall`, and `known search` work in any
directory — the project root is auto-detected from `.git` or build-system
markers and the scope is derived from the root directory name. All data goes
to `~/.known/known.db` by default.

```bash
# Store a fact — works immediately, no init needed; no quotes required
known add The API uses JWT tokens with RS256 signing \
  --source-type conversation --provenance verified

# Recall knowledge (LLM-optimized plain text output)
known recall 'authentication'

# Search with scores and IDs
known search 'authentication' --limit 5

# See what's stored
known stats
known scope tree
```

### Claude Code integration (optional)

Run `known init` to install Claude Code skills and a session-start hook into `.claude/`:

| Skill | Purpose |
|-------|---------|
| `/remember` | Store a fact from the conversation |
| `/recall` | Retrieve knowledge for a query |
| `/forget` | Find and delete an entry |
| `/known-search` | Search with full flag control |

The session-start hook injects the scope tree into context, so the agent knows
what knowledge areas exist without loading content upfront.

`known init` is optional scaffolding — it is never a prerequisite for storing
or retrieving knowledge.


## Architecture

```
cmd/            CLI commands (init, add, recall, search, ...)
cmd/scaffold/   Embedded Claude Code skill templates
model/          Core types: Entry, Edge, Scope, ID (ULID)
storage/        Backend interface + implementations
  sqlite/       Default — pure Go transpiled from C, no CGo
  postgres/     PostgreSQL + pgvector for production scale
query/          Search engine: vector, graph traversal, hybrid
embed/          Embedder interface + backends
  hugot         Default — pure Go ONNX inference, zero config
  ollama        Local Ollama server
  openai        OpenAI-compatible APIs (OpenAI, Azure, vLLM, etc.)
```

### Storage

The default backend is SQLite with in-process vector search. Entries, edges, and
scopes are stored in a single file at `~/.known/known.db`. Each project can
override the DSN in its `.known.yaml` for a per-project database.

PostgreSQL with pgvector is supported for production deployments.

### Embeddings

The default embedder is [hugot](https://github.com/knights-analytics/hugot) —
pure Go ONNX inference using `sentence-transformers/all-MiniLM-L6-v2`. The model
is downloaded to `~/.known/models/` on first use. No API keys, no external services.

For faster embeddings or larger models, configure an external provider:

```bash
# Ollama
export KNOWN_EMBEDDER=ollama
export KNOWN_EMBED_URL=http://localhost:11434
export KNOWN_EMBED_MODEL=nomic-embed-text

# OpenAI-compatible
export KNOWN_EMBEDDER=openai-compatible
export KNOWN_EMBED_URL=https://api.openai.com
export KNOWN_EMBED_API_KEY=sk-...
export KNOWN_EMBED_MODEL=text-embedding-3-small
```

### Scopes

Scopes are hierarchical namespaces derived from directory structure. Working in
`./backend/api/` auto-derives scope `backend.api`. Searching a scope includes all
descendants — searching `backend` returns results from `backend.api`, `backend.storage`, etc.

Projects sharing the same database (the default) can recall across project boundaries
by specifying the other project's scope: `known recall 'auth' --scope otherproject`.

## Commands

```
known init          Optional: install Claude Code skills and write .known.yaml override template
known add           Store a knowledge entry
known update        Modify an existing entry
known delete        Delete an entry
known show          Entry details with relationships
known search        Semantic search with scores and IDs
known recall        LLM-optimized retrieval (plain text)
known related       Graph traversal from an entry
known path          Shortest path between entries
known conflicts     Detect contradictory entries
known link          Create an edge between entries
known unlink        Remove an edge
known scope         Manage scopes (list, create, tree)
known gc            Delete expired entries
known stats         Knowledge graph statistics
known export        Export entries as JSON/JSONL
known import        Import entries from JSON/JSONL
```

Global flags: `--dsn`, `--json`, `--quiet` (placed before the subcommand).

## Configuration

### Project config (`.known.yaml`) — optional override

`.known.yaml` is **not required**. Scope is auto-derived from the project root
marker (`.git`, `go.mod`, etc.) and all commands default to `~/.known/known.db`.

Use `.known.yaml` only when you need to override defaults — custom DSN,
renamed scope prefix, or adjusted thresholds. Write it by hand or generate a
template with `known init`:

```yaml
# Optional: custom database (default: ~/.known/known.db)
# dsn: ~/.known/known.db

# Optional: rename the scope prefix (default: root directory name)
# scope_prefix: myproject

# Optional: tuning overrides
# max_content_length: 4096
# search_threshold: 0.3
```

### Global config (`~/.known/config.yaml`)

Applies to all projects. Same fields as project config, with lower precedence.

### Resolution order

DSN: `--dsn` flag > `KNOWN_DSN` env > project `.known.yaml` > global config > `~/.known/known.db`

## Development

```bash
# Run unit tests
go test ./...

# Run acceptance tests (requires build tag)
go test -tags integration ./test/acceptance/

# Run storage integration tests (requires Docker for PostgreSQL)
go test -tags integration ./storage/postgres/

# Build
go build -o known ./cmd/known/
```

The project uses pure Go throughout — no CGo, no Makefiles, no Docker required
for development. SQLite via [modernc.org/sqlite](https://gitlab.com/cznic/sqlite),
embeddings via [hugot](https://github.com/knights-analytics/hugot) (ONNX on GoMLX).

### Project structure

Tests are colocated with their packages. Acceptance tests in `test/acceptance/`
exercise multi-step workflows against an in-memory SQLite backend. PostgreSQL
integration tests use testcontainers.

Issue tracking uses [beads](https://github.com/anthropics/beads) (`bd` CLI) —
see `AGENTS.md` for the workflow.
