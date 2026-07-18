# bench — known benchmark suite

Build-tagged (`bench`) suite measuring retrieval quality, search
latency/allocations, and whether an LLM agent answers questions better with
`known` recall than without. All files require `-tags bench`; none of this
compiles into the `known` binary.

## Status

The suite is run **manually for now** and is deliberately **not wired into
CI**. It has just landed (`feat/bench-suite`) and needs to be dogfooded by
hand — across the three tiers below — before any tier is trusted as a CI
gate. The hermetic tier is the eventual CI candidate; the full-local and
live-LLM tiers stay manual regardless (they need live network / an API key
that CI sandboxes here do not have). CI wiring is tracked separately and is
out of scope for this document.

## What each layer measures

| Layer | Files | What it measures | When to run |
|---|---|---|---|
| Retrieval scenario suite | `scenarios.go`, `bench_test.go`, `scoring.go` | 10 hand-authored scenarios (A-J: recall, contradiction resolution, scope isolation, graph expansion, FTS rescue, multi-step sessions, provenance, supersede chains, weighted expansion, freshness) scored 0-1 against the real engine + real embedder, plus 3 feature ablations and 3 pipeline-level falsification tests | Full-local tier, after any change to `query/`, `storage/`, or the seed corpus |
| IR quality eval | `metrics.go`, `workload.go`, `scale_test.go` | Graded P@5 / MRR / NDCG@10 over a 1,000-doc synthetic labeled workload (36 queries, graded 0-3 relevance) — hermetic against `FakeEmbedder`, full-fidelity against the real MiniLM embedder | Hermetic tier every run; full-local tier after embedder or ranking-formula changes |
| Latency / alloc benchmarks | `scale_test.go` (`BenchmarkSearchLatency`) | `query.Engine.SearchVector` time/allocs at 1K/5K/10K corpus scale, hermetic (`FakeEmbedder`) | `go test -tags bench -bench Search ./bench/` after storage/query hot-path changes |
| Agent-effectiveness harness | `effectiveness.go`, `runner.go`, `testdata/questions.yaml` | Does an LLM answer codebase questions better with `known recall` output than with none, vs. a full source dump? 3 conditions x 5 sessions x 10 questions. Live runs need `BENCH_API_KEY`/`ANTHROPIC_API_KEY`; hermetic self-tests use a deterministic stub `Answerer` | Live tier after CLI/recall-surface changes worth re-measuring against real models; hermetic self-tests every run |
| CLI capture harness | `bench/capture/` | Structural pass/fail of CLI usability scenarios derived from the friction audit, against a built `known` binary | See `bench/capture/README.md` — separate pre-existing harness, not part of the three tiers below |

## Run tiers

Every command below was executed verbatim in this worktree
(`2e01d2c9157808595247bb57d9d280135824149a`) on 2026-07-18; see the run log
attached to bead `known-ucm` for full output.

### 1. Hermetic (default — no network, no model, no API key)

```sh
go test -tags bench ./bench/...
```

Runs in under 2s. Covers metrics unit tests, the hermetic `FakeEmbedder`
quality eval (`TestQualityEvalFake`), the discrimination self-test, the
effectiveness harness's stub-`Answerer` self-tests, scoring/report unit
tests, and the full `bench/capture` self-test suite. This is the tier
intended for CI once the suite has proven itself; skips everything gated by
`KNOWN_BENCH_FULL` or an API key.

### 2. Full local (real embedder — requires live network, ~100s)

```sh
go run bench/cmd/seedgen/main.go
KNOWN_BENCH_FULL=1 go test -tags bench ./bench/...
```

`seedgen` builds `bench/testdata/seed.db` (107 entries, real hugot/MiniLM
embeddings; gitignored, regenerate any time). The second command then runs
every test including `TestBench` (all 10 scenarios), the 3 ablations, the 3
falsification tests, and `TestQualityEvalReal`.

**Live HF network is required even with a fully populated `~/.known/models`
cache** — hugot performs a model-revision check against Hugging Face on
every run regardless of local cache state, so this tier is never fully
offline. Measured here: `go run bench/cmd/seedgen/main.go` took ~4s,
`KNOWN_BENCH_FULL=1 go test -tags bench ./bench/...` took ~99s (dominated by
`TestQualityEvalReal` at ~93s, which embeds 1,000 documents + 36 queries
with the real model).

Observed this run (commit `2e01d2c9`, `sentence-transformers/all-MiniLM-L6-v2`,
2026-07-18): `TestBench` scenarios A-J all scored 1.000 (OVERALL: 1.000);
`TestQualityEvalReal` mean **P@5=0.956, MRR=1.000, NDCG@10=0.918**; all 3
ablations and all 3 falsification tests passed. These numbers match the
merged-tree provenance already recorded on `RESULTS.md` / bead `known-5qr`
(same P@5/MRR/NDCG figures, `sentence-transformers/all-MiniLM-L6-v2`) —
reproduced independently here rather than merely cited. For canonical,
change-tracked numbers see `RESULTS.md`, not this file (see "Results
provenance" below).

### 3. Live LLM (agent effectiveness — requires API key)

Gated behind `BENCH_API_KEY` (OpenAI-compatible) or `ANTHROPIC_API_KEY`; not
run here (no key available in this environment/sandbox). See `RESULTS.md`
→ "Reproducing" → "Effectiveness benchmark — live LLM rerun" for the exact,
documented rerun command and the full table of environment variables
(`BENCH_MODEL`, `BENCH_BASE_URL`, `BENCH_THINKING`, `BENCH_LIMIT`,
`BENCH_CONCURRENCY`). The hermetic self-tests for this harness (prompt
assembly, all 3 conditions, scoring, report generation via a stub
`Answerer`) run every time under tier 1:

```sh
go test -tags bench ./bench/ -run TestEffectivenessRun_StubAnswerer -v
go test -tags bench ./bench/ -run TestBuildPrompt_ConditionContentDiscrimination -v
```

## Regeneration commands

Each of these was run verbatim in this worktree; see the bead comment log
for full output.

**seedgen** — deterministic retrieval-scenario seed DB (107 entries, real
embedder, requires network):

```sh
go run bench/cmd/seedgen/main.go
```

Produces `bench/testdata/seed.db` (gitignored — regenerate freely, never
commit).

**workloadgen** — derives the 1,000-doc IR-eval labeled query set from
`workload.go`'s topic/fact/query definitions:

```sh
go run -tags bench bench/cmd/workloadgen/main.go
```

Produces `bench/testdata/labeled_queries.json` (checked in — commit after
any `workload.go` change). Verified here with `-out` pointed at a scratch
path: byte-identical to the checked-in file, confirming
`TestLabeledQueriesUpToDate` (which regenerates in-memory and fails the
build on drift) is not currently drifted.

**seed_memory.go** — regenerates `bench/testdata/pipeliner_memory.db`, the
discovered-knowledge fixture the agent-effectiveness harness's `with_memory`
condition queries. Requires the real embedder (network) and a built `known`
binary; run locally only, never inside `go test -tags bench`:

```sh
go build -o /tmp/known-bin ./cmd/known
go run bench/testdata/seed_memory.go | sed 's#^known #/tmp/known-bin #' \
  | KNOWN_DSN=bench/testdata/pipeliner_memory.db sh
```

Verified here against a scratch `KNOWN_DSN` (not the checked-in DB, to avoid
touching testdata as part of this doc-only change): all 49 `known remember`
commands exited 0 and produced a populated SQLite file. See `RESULTS.md`
→ "2026-07 plumbing verification" for why the literal-scope (`--scope
/pipeliner...`) form is required (cwd-dependent scope auto-prefixing broke
the original DB — see bead `known-syk`).

## Results provenance

Numbers belong in `bench/RESULTS.md`, never only in a commit message or
chat transcript. Every table there carries a provenance line: exact binary
commit, model, and date. An unlabeled number in `RESULTS.md` is treated as a
bug. This file (`bench/README.md`) documents *how to run* the suite; it does
not maintain its own separate numbers ledger — the one exception above (the
full-local tier's observed scores) is a same-session reproduction check, not
a competing source of truth.

## Conventions

- **`KNOWN_BENCH_FULL=1`** gates every test that needs the real hugot/MiniLM
  embedder or a generated seed DB (`requireBenchFull` in `bench_test.go`;
  `TestQualityEvalReal` in `scale_test.go`). Unset → skip, never fail.
- **`BENCH_API_KEY`** (OpenAI-compatible, needs `BENCH_MODEL`) or
  **`ANTHROPIC_API_KEY`** gates `TestEffectivenessRun` (`resolveAnswerer` in
  `runner_test.go`). Neither set → skip, never fail.
- **xfail counts in the denominator.** The CLI capture harness's
  `capture_rate = scenarios_passing / total_scenarios` includes xfail
  scenarios as failures rather than excluding them, so the metric moves
  measurably as contracts land instead of pinning at 100% throughout. See
  `bench/capture/README.md` for the full rule and scenario table — this is
  a `bench/capture`-specific convention, not shared by the retrieval/quality
  tiers above (which score scenarios/queries directly, no xfail concept).
- **Falsification + paired `_Pass`/`_Fail` self-tests.** Every predicate and
  every load-bearing pipeline behavior gets a companion test that proves the
  check can actually fail: `bench/capture` predicates ship paired
  `Test<Predicate>_Pass` / `Test<Predicate>_Fail` cases (e.g.
  `TestStoredLabel_Pass` / `TestStoredLabel_Fail_IDLabel`), plus a
  discriminator-oracle test (`TestDiscriminatorOracleFails`) that scores an
  audit-faithful bad stub and asserts it lands well below 100%. The
  retrieval suite mirrors this at the pipeline level: `TestDiscrimination`
  compares real vs. reversed rankings, and
  `TestBenchFalsification_AblationLiftsAreLoadBearing`,
  `TestBenchFalsification_FreshnessAblationFlipsRanking`, and
  `TestSupersedeDemotion_Falsification` (`bench_test.go`) degrade the real
  engine and assert the corresponding score actually drops — a metric that
  can't fail isn't measuring anything.
- **Scenario IDs cite their beads.** Each scenario's doc comment in
  `scenarios.go` names the bead(s) whose behavior it locks in — e.g.
  scenario B / H cite `known-5oq` (supersede demotion) and `known-qam`
  (CLI `--supersedes` flag), scenario I cites `known-1so` (weighted
  expansion ranking formula), scenario J cites `known-oj3`
  (`ObservedAt`-based freshness). Read the comment before changing a
  scenario's queries or expectations — it explains which regression the
  scenario exists to catch, not just what it currently asserts.
