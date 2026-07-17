# known capture-rate harness

Measures CLI usability by running a scenario corpus derived verbatim from the
friction audit (`docs/friction-audit.md`) and scoring each scenario's outcome
against a structural predicate.

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

The harness uses a temporary `HOME` and `KNOWN_DSN` for every scenario so it
never touches `~/.known` or any real database.

## Exit code

- `0` — all non-xfail scenarios passed
- `1` — at least one unexpected failure
- `2` — usage error

## Scenario corpus

Each scenario derives from a specific friction-audit failure mode.

| ID | Audit mode | Description | XFAIL? |
|----|-----------|-------------|--------|
| M2-ulid-first-line | Mode 2 (c1f50adc:110,436) | `add` first stdout line contains ULID | — |
| M2-remember-ulid | Mode 2+3 (c1f50adc:436,1034) | `remember` stdout contains ULID | — |
| M3-nonempty-stdout | Mode 3 (c1f50adc:1034,1427) | stdout non-empty on success | — |
| M4-json-search-ulid | Mode 4 (c1f50adc:1057) | `--json search` `id` field is ULID-format | — |
| M4-json-id-not-integer | Mode 4 | `--json search` `id` is not a bare integer | — |
| M5-unknown-flag-error | Mode 5 (70977423:137) | `--confidence` error mentions a valid flag | XFAIL (wave-2) |
| M6-scope-from-marker-root | Mode 6 (agent-aadf07655847c115b:259) | scope derived from `.known.yaml` | — |
| M6-scope-from-marker-subdir | Mode 6 / zv1.4 | subdir scope appended to prefix | — |
| M2-dedup-second-add | Mode 2 dedup note (70977423:137-139) | second identical add references original ULID | — |
| M4-link-two-entries | Mode 4 link workflow (c1f50adc:1057) | `--link supersedes:<id>` creates an edge | — |

**XFAIL** scenarios are contract tests for zv1.2/zv1.3 wave-2 features.  They
fail on the baseline and epic-current branches (expected); when wave 2 lands
they should flip to PASS.

## Predicates

Predicates match structure, not prose:

- **ULID** — `\b[0-9A-HJKMNP-TV-Z]{26}\b` (Crockford base32, 26 chars)
- **ULID in JSON** — `"id"\s*:\s*"[0-9A-HJKMNP-TV-Z]{26}"`
- **Scope** — substring match on the `Scope:` line

## Committed results

| File | Binary provenance | Capture rate |
|------|-------------------|-------------|
| `results/baseline-a59396f.json` | a59396f (pre-wave-2 merge base) | 9/9 (100%) + 1 XFAIL |
| `results/epic-current.json` | d5dd65f (zv1.4 merged) | 9/9 (100%) + 1 XFAIL |

Both binaries score 9/9 on the non-XFAIL scenarios.  The remaining XFAIL
(`M5-unknown-flag-error`) will flip to PASS once zv1.2 improves the error
message for unknown flags.

## Re-running after wave 2 lands

```sh
go build -o /tmp/known-w2 ./cmd/known
go run ./bench/capture \
  -bin /tmp/known-w2 \
  -commit $(git rev-parse HEAD) \
  -out bench/capture/results/wave2.json
```

`M5-unknown-flag-error` should move from XFAIL to PASS, raising the capture
rate denominator by 1.

## Self-tests

The harness ships unit tests that verify predicates discriminate pass/fail
using a stub binary:

```sh
go test ./bench/capture/...
```
