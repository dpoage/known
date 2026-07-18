# known capture-rate harness

Measures CLI usability by running a scenario corpus derived verbatim from the
friction audit (`docs/friction-audit.md`) and scoring each scenario against a
structural predicate.

**Before/after summary (zv1 epic):**

| Binary | Commit | Capture rate |
|--------|--------|-------------|
| Baseline (pre-wave-2) | a59396f | **4/10 (40%)** |
| Epic (zv1.2 + zv1.3 + zv1.4 merged) | 8c122be | **10/10 (100%)** |

All 10 scenarios pass on 8c122be (corpus hash `6e49bfe0da86`).
Regenerate results after future waves with the one-liner below.

## Quick start

```sh
go run ./bench/capture -bin /path/to/known
```

With results saved:

```sh
go run ./bench/capture \
  -bin /path/to/known \
  -commit $(git rev-parse HEAD) \
  -out bench/capture/results/run.json
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-bin` | (required) | Path to the `known` binary under test |
| `-out` | (none) | Write JSON results to this file |
| `-commit` | (none) | Commit hash to embed in JSON results |
| `-model-cache` | `~/.known/models` | Path to model cache dir (symlinked into each temp HOME) |

The harness uses a temporary `HOME` and `KNOWN_DSN` for every scenario; it
never touches `~/.known` or any real database.  The shared model cache
directory is symlinked into each temp HOME so the embedder finds the cached
files.  **Network access to Hugging Face is required** — hugot contacts HF on
each run regardless of the local cache; repeated runs may be rate-limited.

## Exit code

- `0` — all non-xfail scenarios passed
- `1` — at least one unexpected failure
- `2` — usage error

## Capture-rate metric

```
capture_rate = scenarios_passing / total_scenarios
```

**xfail scenarios count in the denominator as failures**, so the metric
honestly reads below 100% on any binary that does not yet implement the
wave-2 contracts.  When a wave lands, its scenarios flip xfail→xpass and the
rate rises measurably.  Excluding xfail from the denominator would pin the
metric at 100% before and after every wave — making improvement invisible.

## Scenario corpus

Each scenario derives from a specific friction-audit failure mode.

| ID | Audit mode | Description | XFAIL? |
|----|-----------|-------------|--------|
| M2-compact-confirmation | Mode 2 (c1f50adc:110,436) | add output is compact (≤4 non-empty lines, no embedding boilerplate) | zv1.2 |
| M2-tail2-visible-ulid | Mode 2 (c1f50adc:436) | ULID on first line OR output ≤3 non-empty lines (regression guard, not XFAIL) | — |
| M2-stored-label | Mode 2 — zv1.2 confirmation contract | first line uses `Stored`/`Duplicate` not `ID:` | zv1.2 |
| M2-dedup-explicit | Mode 2 dedup note (70977423:137-139) | second identical add prints `Duplicate <ULID>` | zv1.2 |
| M3-nonempty-with-ulid | Mode 3 (c1f50adc:1034,1427) | stdout non-empty and contains ULID on success | — |
| M4-link-by-content | Mode 4 (c1f50adc:1057) / zv1.3 | link by exact content query creates edge, no ULIDs typed | zv1.3 |
| M4-link-accept-subcommand | Mode 4 / zv1.3 link-accept | `link accept "<query>" --all` subcommand exists | zv1.3 |
| M5-unknown-flag-suggests-valid | Mode 5 (70977423:137) / zv1.2 | `--confidence` error names a valid alternative flag | zv1.2 |
| M6-scope-from-marker | Mode 6 / zv1.4 | scope from `.known.yaml` `scope_prefix` (regression guard) | — |
| M6-scope-subdir-appended | Mode 6 / zv1.4 | subdir scope appended to prefix (regression guard) | — |

**XFAIL** — expected to fail; contract not yet fully implemented.
Scenarios marked `zv1.2`/`zv1.3` flipped to XPASS at 8c122be.

Note: `M2-tail2-visible-ulid` predicate relaxed per oracle ruling (over-literal):
passes if ULID is on the first output line OR output is ≤3 non-empty lines.
M2-compact-confirmation already covers the embedding-boilerplate friction.

## Predicates

Predicates match structure, not prose:

- **ULID** — `\b[0-9A-HJKMNP-TV-Z]{26}\b` (Crockford base32, 26 chars)
- **compact** — ≤4 non-empty lines in stdout
- **Stored/Duplicate label** — first non-empty line starts with `Stored` or `Duplicate`
- **Edge created** — stdout contains literal `Edge created.`
- **Scope** — substring match on the scope line

## Committed results

| File | Binary | Build command | Capture rate |
|------|--------|---------------|-------------|
| `results/baseline-a59396f.json` | a59396f | `git archive a59396f \| tar -x -C /tmp/bl && go build -o /tmp/bl/known-baseline /tmp/bl/cmd/known` | 4/10 (40%) |
| `results/epic-interim-3ccf253.json` | 3ccf253 (pre-merge) | `go build ./cmd/known` in worktree | 8/10 (80%) — interim, corpus hash differed |
| `results/epic-8c122be.json` | 8c122be (zv1.2+zv1.3+zv1.4) | binary at `/tmp/known-epic` | **10/10 (100%)** — definitive after |

The interim result (3ccf253) is kept for traceability but used a different
corpus hash; 8c122be is the authoritative post-wave-2 measurement.

## Re-running

```sh
go run ./bench/capture \
  -bin /path/to/known \
  -commit $(git rev-parse HEAD) \
  -model-cache ~/.known/models \
  -out bench/capture/results/run.json
```

All 10 scenarios pass on 8c122be.  Predicate nits resolved in this commit:
`M2-tail2-visible-ulid` relaxed (oracle ruling: over-literal); now a regression
guard (not XFAIL).  `M4-link-by-content` uses exact stored content so the
resolver's exact-match path fires.

## Self-tests

The harness ships unit tests that verify predicates discriminate pass/fail
using stub binaries, including a discrimination oracle that verifies an
audit-faithful bad stub scores significantly below 100%:

```sh
go test ./bench/capture/...
```
