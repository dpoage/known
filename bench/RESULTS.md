# Benchmark Results

First full run: 2026-03-18
Model: MiniMax-M2.5 via Anthropic-compatible API
Questions: 50 across 5 sessions, 3 conditions

## Agent Effectiveness

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
  discovered facts stored by walking the codebase)
- **Full Dump**: LLM receives all source files concatenated (~1800 LOC)

### Key Findings

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

## Retrieval Quality

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

### Retrieval benchmark (fast, no API key needed)

```bash
go test -tags bench ./bench/... -run TestBench -v
```

### Effectiveness benchmark (requires API key)

```bash
ANTHROPIC_API_KEY=<key> \
BENCH_MODEL=MiniMax-M2.5 \
BENCH_BASE_URL=https://api.minimax.io/anthropic \
BENCH_CONCURRENCY=5 \
  go test -tags bench ./bench/ -run TestEffectivenessRun -v -timeout 30m
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
  runner_test.go          # Effectiveness benchmark harness
  baseline.go             # JSON baseline persistence + regression detection
  cmd/seedgen/main.go     # Deterministic seed DB generator (85 entries, 32 edges)
  testdata/
    seed.db               # Generated retrieval benchmark DB
    pipeliner_memory.db   # Discovered knowledge for with_memory condition
    questions.yaml         # 50 questions across 5 sessions
    seed_memory.go         # Alternative: hand-authored memory seed facts
    codebase/              # Synthetic "pipeliner" Go project (18 files, 3 bugs)
```
