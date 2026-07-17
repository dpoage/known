# known capture-rate harness

Measures CLI usability by running a scenario corpus derived verbatim from the
friction audit (`docs/friction-audit.md`) and scoring each scenario against a
structural predicate.

**Before/after summary (zv1 epic):**

| Binary | Commit | Capture rate |
|--------|--------|-------------|
| Baseline (pre-wave-2) | a59396f | **3/10 (30%)** |
| Epic (zv1.2 + zv1.3 + zv1.4 merged) | 8c122be | **8/10 (80%)** |

Two scenarios remain XFAIL pending further work (see corpus table).
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
| `-model-cache` | `~/.known/models` | Path to shared model cache (avoids per-scenario downloads) |

The harness uses a temporary `HOME` and `KNOWN_DSN` for every scenario; it
never touches `~/.known` or any real database.  The shared model cache
directory is **symlinked** (not copied) into each temp HOME, so the embedder
skips network downloads and two consecutive runs produce identical results.

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
| M2-tail2-visible-ulid | Mode 2 (c1f50adc:436) | ULID visible after `\| tail -2` | zv1.2+ |
| M2-stored-label | Mode 2 — zv1.2 confirmation contract | first line uses `Stored`/`Duplicate` not `ID:` | zv1.2 |
| M2-dedup-explicit | Mode 2 dedup note (70977423:137-139) | second identical add prints `Duplicate <ULID>` | zv1.2 |
| M3-nonempty-with-ulid | Mode 3 (c1f50adc:1034,1427) | stdout non-empty and contains ULID on success | — |
| M4-link-by-content | Mode 4 (c1f50adc:1057) / zv1.3 | link creates edge by content query, zero ULIDs typed | zv1.3 |
| M4-link-accept-subcommand | Mode 4 / zv1.3 link-accept | `link accept "<query>" --all` subcommand exists | zv1.3 |
| M5-unknown-flag-suggests-valid | Mode 5 (70977423:137) / zv1.2 | `--confidence` error names a valid alternative flag | zv1.2 |
| M6-scope-from-marker | Mode 6 / zv1.4 | scope from `.known.yaml` `scope_prefix` (regression guard) | — |
| M6-scope-subdir-appended | Mode 6 / zv1.4 | subdir scope appended to prefix (regression guard) | — |

**XFAIL** — expected to fail; contract not yet fully implemented.
Scenarios marked `zv1.2`/`zv1.3` flipped to XPASS at 8c122be.

Note: `M2-tail2-visible-ulid` remains XFAIL even on zv1.2 because the
compact 3-line output still puts the ULID on line 1 of 3 (not in `tail -2`).
The real fix is that agents should not pipe `add` output through `tail` at
all — M2-compact-confirmation proves the output is short enough to read whole.

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
| `results/baseline-a59396f.json` | a59396f | `git archive a59396f \| tar -x -C /tmp/bl && go build -o /tmp/bl/known-baseline /tmp/bl/cmd/known` | 3/10 (30%) |
| `results/epic-interim-3ccf253.json` | 3ccf253 (pre-merge bench worktree HEAD) | `go build -o /tmp/known-epic ./cmd/known` | 8/10 (80%) — interim, zv1.2/zv1.3 not yet merged |
| `results/epic-8c122be.json` | 8c122be (zv1.2+zv1.3+zv1.4 merged) | binary at `/tmp/known-epic` | **8/10 (80%)** — definitive after |

The interim result (3ccf253) is kept for traceability; 8c122be is the
authoritative post-wave-2 measurement.

## Re-running

```sh
go run ./bench/capture \
  -bin /path/to/known \
  -commit $(git rev-parse HEAD) \
  -model-cache ~/.known/models \
  -out bench/capture/results/run.json
```

Remaining XFAIL scenarios to watch:
- `M2-tail2-visible-ulid`: ULID in last 2 output lines — passes only if output
  is ≤2 non-empty lines (currently 3 in zv1.2; agents should read full output).
- `M4-link-by-content`: content resolver returns ambiguity when two entries
  share vocabulary; needs stricter disambiguation or exact-match preference.

## Self-tests

The harness ships unit tests that verify predicates discriminate pass/fail
using stub binaries, including a discrimination oracle that verifies an
audit-faithful bad stub scores significantly below 100%:

```sh
go test ./bench/capture/...
```
