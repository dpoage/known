# Known — Project Identity

## What this is

**known** is a persistent memory graph for LLM agents. It is a local-first Go CLI
that stores atomic facts during one session and recalls them in the next, so agents
stop re-learning the same codebase conventions, decisions, and user preferences
after every context compaction.

The one-sentence test for whether something belongs in known:

> "Will an agent need this fact in two weeks, and is it not already in code or docs?"

### Positioning

| Tool | Remembers | Unit |
|------|-----------|------|
| **beads** (`bd`) | Work: tasks, blockers, decisions-in-progress | Issue |
| **known** | Knowledge: facts, conventions, environment, architecture | Entry |

They are complements, not competitors. A bead says *"migrate the auth table"*;
a known entry says *"all new tables use ULIDs, not UUIDs."*

### The problem right now

The pipes work; the ergonomics don't. In practice, agents driving known through a
harness (Claude Code, opencode, Pi) **frequently fail to store the facts they
learn**, and building the internal graph of dependencies and links is difficult
enough that it mostly doesn't happen. A memory system that isn't written to is
dead weight. This is the central identity issue of the current development
effort: the CLI was designed storage-out, and it must be redesigned agent-in.
The capture and graph-building surface is expected to be rethought, not patched.

## Core model

- **Entry** — one atomic fact (≤4096 bytes), ULID-identified, deduplicated by
  content-hash + scope, embedded for semantic search.
- **Edge** — typed, directed, weighted relationship: `depends-on`, `contradicts`,
  `supersedes`, `elaborates`, `related-to` (custom types allowed). Edge weights
  are reinforced by session usage and applied during graph expansion.
- **Scope** — dot-separated hierarchical namespace auto-derived from directory
  structure (`backend/api/` → `backend.api`). Searches include descendants;
  `scope_prefix` in `.known.yaml` qualifies a project; leading `/` crosses projects.
- **Provenance** — assertion strength: `verified` (human-stated), `inferred`
  (agent-derived), `uncertain`. Separate from **freshness** (ObservedAt/ObservedBy/
  SourceHash), which tracks *when* a fact was last confirmed against its source.

## Design principles

1. **Agent-first ergonomics.** The primary user is an agent inside a harness, not
   a human at a prompt. Every capture command must succeed in one shot with
   minimal context: forgiving inputs, no required ceremony (flags an agent won't
   remember mid-task), errors that state the fix, and output that suggests the
   next action (e.g. probable links). If storing a fact costs more attention than
   the fact is worth, agents won't do it — and measurably, they don't.
2. **Local-first, zero-config.** The default path — pure-Go SQLite
   (`modernc.org/sqlite`) at `~/.known/known.db`, pure-Go ONNX embeddings
   (hugot, MiniLM-L6-v2) — requires no CGo, no API keys, no services, no Docker.
   PostgreSQL + pgvector exists for scale, never as a prerequisite.
3. **LLM-first output.** `recall` emits plain text shaped for direct context
   injection; `search` emits scores and IDs for programmatic use. The session-start
   hook injects only the scope *tree* — knowledge areas, not content — so context
   cost is paid on demand.
4. **Atomic facts, not documents.** known is not a RAG pipeline. The 4KB content
   cap is a feature: it forces one fact per entry, which keeps dedup, conflict
   detection (`contradicts` edges), and recall precision meaningful.
5. **Retrieval is hybrid.** Vector similarity, graph expansion over weighted edges,
   and FTS5 full-text are combined signals. Ranking quality is a core competency
   of this project.
6. **Trust is explicit.** Every entry carries provenance and freshness. An agent
   consuming recall output can weigh "verified, fresh" differently from
   "inferred, stale 143d". Never present stale inference as truth.
7. **Make invalid states unrepresentable.** Typed IDs (ULID), typed provenance,
   optimistic concurrency via versions, sentinel errors (`ErrNotFound`,
   `ErrDuplicateContent`, `ConcurrentModificationError`).
8. **Dogfooding.** This repo tracks its own work in beads and stores its own
   architecture in known. If using known on known feels bad, that is a P1 bug
   in product terms.

## Non-goals

- **Not an issue tracker.** That is beads.
- **Not a document store / RAG service.** No chunking pipelines, no PDF ingestion.
- **Not a SaaS.** No hosted offering, no telemetry, no accounts. HTTP server mode
  (`known serve`), if it ever lands, is a local convenience, not a product pivot.
- **Not a daemon by default.** Every command is a short-lived process; latency
  work targets cold-start cost rather than resident services.
- **Not a general vector database.** Embeddings serve fact recall; we do not chase
  vector-DB feature parity.

## Distribution

Three integration surfaces, one binary:

1. **CLI** — `go install github.com/dpoage/known/cmd/known@latest`.
2. **Claude Code plugin** (`plugin/`) — marketplace-installable, namespaced
   skills (`/known:remember`, `/known:recall`, `/known:forget`, `/known:search`).
3. **Standalone scaffold** (`known init`) — per-project skills + session hook.

Plugin and scaffold share skill content; divergence between them is a bug
(see git history: "Unify skill guidance across plugin and standalone install paths").

## Current goals (2026 H2)

Derived from the open beads backlog (`bd ready`); themes in priority order:

1. **Agent-first capture UX (the rethink, epic known-zv1).** Redesign the
   store-and-link surface around how agents actually behave in harnesses. Facts
   must get captured as a side effect of normal agent work, and graph edges must
   be buildable without the agent hand-assembling IDs and edge types — candidate
   directions include auto-linking on add (embedding-similarity suggestions),
   batch capture, and accept-anything input parsing (known-zv1.1–.3). Setup
   ceremony goes too: auto-derive project scope from root markers (.git, go.mod,
   Makefile, CMakeLists.txt, BUILD, requirements.txt, ...) against the
   system-wide db, demoting `.known.yaml` to an optional override — or removing
   it (known-zv1.4). This theme gates and reshapes everything below; retrieval
   quality is moot if nothing gets stored.
2. **Retrieval correctness** — unify scoring across vector and expansion results
   (known-1so), total limit + final sort in SearchHybrid (known-6hj), FTS as a
   third retrieval signal (known-2s3), freshness from ObservedAt (known-oj3),
   reinforcement weight decay (known-906).
3. **Robustness** — nil-embedder guard (known-mrc), transactional entry+link
   creation (known-7dw), HTTP timeouts/body limits in embedding providers
   (known-35b, known-2qr, known-cqh), SQLite scope-hierarchy bug (known-aqb).
4. **Prove effectiveness** — the `bench/` testbed (known-oqa): measurable evidence
   that agents with known outperform agents without it. This gates any marketing
   of the tool.
5. **Ship v0.1.0** — CI (known-ha2), goreleaser (known-16a), release workflow
   (known-7qg), prebuilt binaries (known-vvo), tag (known-rd5).
6. **Performance** — ANN for SQLite (known-7fn), SIMD distance (known-30u),
   precomputed norms (known-d8b) — only after correctness themes land.
7. **Provider breadth** — Gemini/Cohere/Mistral adapters under epic known-9p1 —
   lowest priority; the default hugot path must stay zero-config.

## Architecture map

```
cmd/            CLI (pflag-based dispatch, lazy embedder init)
cmd/scaffold/   Embedded Claude Code skill templates
model/          Entry, Edge, Scope, ID (ULID), Provenance, Freshness
storage/        Repo interfaces (EntryRepo, EdgeRepo, ScopeRepo, Backend)
  sqlite/       Default: pure Go, WAL, single-writer, two-pass vector search
  postgres/     pgx/v5 + pgvector, testcontainers integration tests
query/          Hybrid engine: vector + graph expansion + FTS, reinforcement
embed/          Embedder interface: hugot (default), ollama, openai-compatible
plugin/         Claude Code plugin (marketplace distribution)
test/           CLI + acceptance tests (in-memory SQLite workflows)
bench/          Effectiveness testbed (in progress)
```
