# Benchmark Results

## Provenance

Every table in this document is labeled with the exact binary commit, model,
and date it was measured against. **Unlabeled numbers are a bug** — if you
add a table here, add its provenance line with it.

## Agent Effectiveness

> **Provenance: 2026-03-18, commit `df7bb04`-era binary (pre-#36), model
> MiniMax-M2.5.**
> **STALE — CLI surface has since changed (PRs #36-#41: agent-first output,
> memory-verb triad, recall changes). These numbers are the historical
> baseline only; they are not valid for the current CLI and a rerun is
> required before citing them as current state.** See "Reproducing" below
> for the exact rerun command (gated on `BENCH_API_KEY`/`ANTHROPIC_API_KEY`
> — no key is available in CI/agent sandboxes, so this table has not been
> refreshed as part of the 2026-07 bench-suite round).

Does giving an LLM access to `known` (stored knowledge from codebase discovery)
improve its ability to answer questions about a codebase?

| Session | No Memory | With Memory | Full Dump | Delta |
|---------|-----------|-------------|-----------|-------|
| 1: Onboarding | 0.40 | 0.70 | 0.70 | **+0.30** |
| 2: Feature Work | 0.30 | 0.50 | 0.90 | **+0.20** |
| 3: Bug Investigation | 0.30 | 0.80 | 0.90 | **+0.50** |
| 4: Refactoring | 0.00 | 0.50 | 0.90 | **+0.50** |
| 5: Code Review | 0.20 | 0.20 | 0.60 | +0.00 |
| **Overall** | **0.24** | **0.54** | **0.80** | **+0.30** |

### Conditions

- **No Memory**: LLM receives only a file listing (filenames, no content)
- **With Memory**: LLM receives `known recall` output per question (from 35
  discovered facts stored by walking the codebase, March seed)
- **Full Dump**: LLM receives all source files concatenated (~1800 LOC)

### Key Findings (as measured pre-#36 — subject to change on rerun)

1. **Memory more than doubles no-memory performance** (0.54 vs 0.24).

2. **The delta increases with session depth.** Sessions 3-4 (bug investigation,
   refactoring) show +0.50 lift — these require cross-file reasoning where
   accumulated knowledge matters most.

3. **Memory achieves 67% of full-dump performance** (0.54 vs 0.80) without
   needing the entire codebase in context. For agents operating under context
   limits, this is the core value proposition.

4. **Session 5 (code review) shows no memory lift.** These questions combine
   knowledge from all prior sessions and are genuinely hard across all conditions.

5. **Full dump is the ceiling at 0.80**, not 1.00 — some questions require
   reasoning the model cannot do in a single prompt regardless of context.

### 2026-07 plumbing verification (no rerun — recall path only)

The with_memory condition's plumbing (`bench/runner.go` prompt assembly +
`known recall` invocation, `bench/testdata/pipeliner_memory.db`) was verified
against the **current** binary built from commit `bac1839` (`feat/bench-suite`,
2026-07-17) without an API key — this checks that the recall command still
runs and returns real results, not that the LLM scores above changed:

```
$ KNOWN_DSN=bench/testdata/pipeliner_memory.db known recall \
    "How does config.Load handle overrides vs defaults" \
    --scope /pipeliner --limit 10 --threshold 0.3
[pipeliner.config] (inferred, source: cli, fresh) {01KXSSV8P6TANPCPGAB38T04YC}
BUG: config/loader.go Load() calls loadOverrides THEN loadDefaults — defaults overwrite production overrides

[pipeliner.config] (inferred, source: cli, fresh) {01KXSSVJRNG2JR6HD4XG3QVF77}
deriveOverridePath in loader.go converts config/default.yaml to config/production.yaml automatically
...
```

Findings (full root-cause analysis recorded on bead `known-syk`):

- `known recall`'s flags (`--scope`, `--limit`, `--threshold`) are unchanged
  by #36-#41 — the CLI surface itself did not break this path.
- The committed `pipeliner_memory.db` **did** have schema/state drift: its
  entries were stored under scope `known.pipeliner*` (auto-prefixed from the
  directory name of wherever `known remember` happened to run in March), plus
  a stray empty top-level `pipeliner` scope row that shadowed the real data
  during scope resolution. Querying with the harness's literal `--scope
  pipeliner` returned "No matching knowledge found" 100% of the time,
  independent of query — a real, reproducible break.
- Fixed by regenerating `pipeliner_memory.db` deterministically (see
  `bench/testdata/seed_memory.go`) using the literal-scope escape hatch
  (`--scope /pipeliner...`), which is immune to the cwd-dependent scope
  auto-prefixing that broke the original DB. `bench/runner.go`'s with_memory
  `RecallCommand` now queries `--scope /pipeliner` for the same reason.
  Regeneration procedure:

  ```bash
  # From any directory (literal scope is cwd-independent). Requires the real
  # MiniLM embedder (model cache at ~/.known/models) — NOT hermetic, run
  # locally only, never inside `go test -tags bench`.
  go build -o /tmp/known-bin ./cmd/known
  go run bench/testdata/seed_memory.go | sed 's#^known #/tmp/known-bin #' \
    | KNOWN_DSN=bench/testdata/pipeliner_memory.db sh
  ```

## Retrieval Quality

> **TODO (bead `known-58u`, owner: retrieval-scoring slice): the table below
> is a PLACEHOLDER, not final data.** It reflects the pre-round suite as
> committed at `bac1839` (`feat/bench-suite` merge, 2026-07-17). Running it
> (`go test -tags bench ./bench/... -run TestBench -v`) requires the real
> embedder and is gated behind `KNOWN_BENCH_FULL=1` (skipped by default) —
> it needs live network access for the embedder's model-revision check even
> with a fully populated local model cache, so it is **not hermetic**, unlike
> the effectiveness self-tests below. Every scenario is saturated at 1.000 —
> a saturated metric has no headroom and cannot detect regressions or prove
> discriminating power. known-58u is expanding the seed corpus with
> distractors and adding new scenarios (supersede chains, weighted-expansion
> ranking, FTS near-miss distractors) specifically to restore headroom.
> **Do not cite this table as current results** — replace it (scenario
> table, ablation lifts, and commit hash) with known-58u's final numbers
> before this document is treated as authoritative.

How well does the search pipeline find the right facts?

```
Scenario                                  Score  Queries
------------------------------------------------------------
A: Codebase Discovery Recall              1.000  4/4
B: Contradiction Resolution               1.000  3/3
C: Scope Isolation                        1.000  3/3
D: Needle-in-Haystack with Graph          1.000  2/2
E: FTS Rescue                             1.000  4/4
F: Multi-Step Session                     1.000  4/4
G: Provenance Trust                       1.000  3/3

OVERALL: 1.000
```

### Feature Ablation

> **TODO (bead `known-58u`): placeholder, see note above — pre-round numbers
> as committed at `bac1839` (`feat/bench-suite` merge, 2026-07-17), hermetic.**

What happens when individual features are disabled?

| Feature | Full | Without | Lift |
|---------|------|---------|------|
| Graph Expansion | 1.000 | 0.982 | +0.018 |
| FTS5 Fusion | 1.000 | 0.987 | +0.013 |
| Freshness Weighting | 1.000 | 1.000 | +0.000 |

- **Graph Expansion**: query "deployment process" loses the linked SQLite
  storage entry without expansion edges.
- **FTS5**: vector search for "ALPHA-4091" returns the wrong error code;
  only text search finds the exact match.
- **Freshness**: recency weighting cannot overcome large cosine similarity
  gaps in the current seed data. This is a real product finding — the
  recency formula may need tuning for contradiction resolution.

## Reproducing

### Hermetic effectiveness self-tests (no API key, no network, no model)

Proves prompt assembly (including per-condition content, not just that each
condition "runs"), all three conditions, scoring, and report generation work
end to end via a deterministic stub Answerer:

```bash
go test -tags bench ./bench/ -run TestEffectivenessRun_StubAnswerer -v
go test -tags bench ./bench/ -run TestBuildPrompt_ConditionContentDiscrimination -v
```

### Retrieval benchmark (requires live network — NOT hermetic)

Requires the real embedder and is gated behind `KNOWN_BENCH_FULL=1` (skipped
by default): it needs live network access for the embedder's model-revision
check even with a fully populated local model cache (`~/.known/models`).

```bash
KNOWN_BENCH_FULL=1 go test -tags bench ./bench/... -run TestBench -v
```

### Effectiveness benchmark — live LLM rerun (requires API key)

**This is the command to run to refresh the stale March table above against
the current CLI surface.** Not run as part of this round (no API key
available in this environment):

```bash
ANTHROPIC_API_KEY=<key> \
BENCH_MODEL=MiniMax-M2.5 \
BENCH_BASE_URL=https://api.minimax.io/anthropic \
BENCH_CONCURRENCY=5 \
  go test -tags bench ./bench/ -run '^TestEffectivenessRun$' -v -timeout 30m
```

Environment variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `ANTHROPIC_API_KEY` | API key (Anthropic-compat providers) | required |
| `BENCH_MODEL` | Model name | claude-haiku-4-5-20251001 |
| `BENCH_BASE_URL` | API base URL | https://api.anthropic.com |
| `BENCH_THINKING` | Enable extended thinking (set to "1") | off |
| `BENCH_LIMIT` | Max questions per condition | all 50 |
| `BENCH_CONCURRENCY` | Parallel API calls | 1 |
| `BENCH_API_KEY` | OpenAI-compat API key (alternative) | - |

## Architecture

```
bench/
  scoring.go              # 5-dimension weighted scoring
  report.go               # Terminal report formatting
  scenarios.go            # 7 retrieval scenarios + ablation configs
  bench_test.go           # Retrieval benchmark harness
  effectiveness.go        # Question loading, answer checking, comparison
  runner.go               # LLM answerer interface + API implementations
  runner_test.go          # Effectiveness benchmark harness + stub Answerer self-tests
  baseline.go             # JSON baseline persistence + regression detection
  cmd/seedgen/main.go     # Deterministic seed DB generator (85 entries, 32 edges)
  testdata/
    seed.db               # Generated retrieval benchmark DB
    pipeliner_memory.db   # Discovered knowledge for with_memory condition
                           # (regenerate via seed_memory.go — see procedure above)
    questions.yaml         # 50 questions across 5 sessions
    seed_memory.go         # `known remember` command generator that seeds pipeliner_memory.db
    codebase/              # Synthetic "pipeliner" Go project (18 files, 3 bugs)
```
