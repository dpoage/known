# Known Capture Friction Audit

**Date:** 2026-07-17  
**Branch:** zv1.1-friction-audit  
**Bead:** known-zv1.1

---

## Methodology

### Sources examined

| Harness | Storage | Sessions examined |
|---------|---------|-------------------|
| Claude Code | `~/.claude/projects/**/*.jsonl` | 236 JSONL files across 8 project dirs |
| opencode | `~/.local/share/opencode/opencode.db` (SQLite `message` table) | All rows |
| omp/Pi | `~/.omp/logs/*.log` (structured JSON log lines) | 4 log files (2026-06-16 through 2026-07-17) |

### Search patterns

```
known add | known remember | known link | known search
known recall | known init | /known: | known forget | known update
```

Matching JSONL lines were parsed with Python (`json.loads`); the surrounding
±4 lines were read to capture tool results and agent follow-up. SQLite rows
were queried with `LIKE '%known add%' OR …`. Log files were grepped directly.

### Corpus summary

- **Claude Code:** 7 of 236 JSONL files contained any known pattern. All 7 were
  in the `services-runtime` project (the only project observed to have `known
  init` run). Zero matches in 8 other project directories.
- **opencode:** 0 messages in the `message` table matched. known is not
  installed or never invoked in any opencode session on this machine.
- **omp/Pi:** 0 known-command log lines. However, every session log contains
  a known-plugin YAML parse failure (see Mode 7 below).

Total captured incidents: **17** across **4 JSONL files** (1 main session,
1 subagent session, 2 other sessions) — all in Claude Code, all in one project.

---

## Ranked Failure Modes

### Mode 1 — Missed capture (the dominant problem)

**Count: 52 sessions (93% of 56 session files in the instrumented project)**

The agent performed substantive work — architectural decisions, root-cause
diagnoses, code design choices — and stored nothing. 105 fact-bearing assistant
messages were identified in one sampled 4.5 MB session (a5405027) that had zero
known invocations.

**Evidence — session a5405027 (4.5 MB, zero known calls):**

```
[line 49 assistant]: Confirmed on all counts. The verification is decisive:
- ServiceRuntime() takes no Configuration (Engine.hpp:51) — the engine literally
  cannot be configured because config is never injected into it.
- Game::configure() reads config and throws it away — it logs engine:name and
  window_width, then never applies them. Only world:default_level is actually used.
```

```
[line 52 assistant]: ## How data-driven is the engine today? About 10%.
I measured rather than assumed, and the finding is sharp and a little surprising:
you have excellent data-driven infrastructure that is almost entirely unplugged.
```

```
[line 79 assistant]: Key things I've already spotted with certainty:
- CallbackRegistry.cpp:304 + remove_callback (314-330) is a use-after-free /
  dangling-pointer bug...
```

None of these were stored. The agent moved on to the next task.

**Evidence — session ce3a06d8 (315 KB, zero known calls):**

```
[line 39 assistant]: These checks corrected two more agent claims:
- network_event_manager.update() IS called every frame (line 153) — the second
  agent was wrong that it's missing from the loop.
```

**Root cause:** Agents do not spontaneously use `known remember` mid-task. They
use it when the skill explicitly prompts them or when the task description
mentions "record findings". Without that nudge — which requires `known init` and
active skill injection — capture does not happen.

---

### Mode 2 — Output buries result in embedding boilerplate

**Count: all successful `known remember` invocations**

A successful store prints a multi-line response. When agents piped to `2>&1 |
tail -2` or `tail -3`, they saw only the embedding model line — no confirmation
of what was stored, no ID, no suggested links.

**Evidence — session c1f50adc, line 110:**

Command:
```sh
known remember "DeferredRenderer distance field target..." --scope engine.graphics 2>&1 | tail -3
```

Tool result visible to agent:
```
Expires:    2026-09-03T19:07:26Z
Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)
```

**Evidence — session c1f50adc, line 436:**

Command:
```sh
known remember "In radiance_cascades.frag direct lighting..." --scope engine.graphics 2>&1 | tail -2
```

Tool result:
```
Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)
```

The agent cannot confirm: (a) what was stored, (b) which ID was assigned, (c)
whether dedup fired, or (d) what related entries exist. The output teaches
nothing and suggests no next action.

---

### Mode 3 — Silent success (no output at all)

**Count: 2 incidents**

When `known remember` was run without a pipe, the bash tool returned `(Bash
completed with no output)`. The agent received no confirmation whatsoever.

**Evidence — session c1f50adc, line 1034:**

Command:
```sh
cd /home/dustin/code/personal/services-runtime
# Record the architecture decision in known (survives sessions; explains WHY)
known remember "Renderer architecture decision (2026-06): 3D will be a SIBLING
renderer under a runtime RendererInterface..." --scope engine.graphics
```

Tool result:
```
(Bash completed with no output)
```

**Evidence — session c1f50adc, line 1427:**

Same pattern — `known remember "Renderer decouple design..."` returned `(Bash
completed with no output)`. The Claude Code harness suppresses stdout when it
matches its "no output" heuristic. The agent continued without knowing if the
store succeeded.

*Note: this may be a Claude Code harness artifact (stdout suppression for
"silent" commands), but the effect is indistinguishable from a failure.*

---

### Mode 4 — ID format mismatch / link never created

**Count: 1 incident (but blocks all supersede/update workflows)**

The agent tried to search for an existing entry to supersede it, extracted the
ID with a regex that assumes integer IDs, got an empty result, and stored a
correction entry without linking it to the superseded one.

**Evidence — session c1f50adc, line 1057:**

Command:
```sh
ID=$(known --json search 'renderer architecture decision sibling RendererInterface
    submit_geometry' --scope engine.graphics 2>/dev/null \
    | grep -o '"id":[0-9]*' | head -1 | grep -o '[0-9]*')
echo "found entry id: $ID"
known remember "CORRECTION to renderer decoupling decision..."
```

Tool result:
```
found entry id: 
```
(empty — ULID `01KTD44NBEHMHE0MPY6VGFCJR6` does not match `[0-9]*`)

Consequence: the original entry (`Renderer architecture decision`) and the
correction (`CORRECTION to renderer decoupling decision`) exist as independent
entries with no `supersedes` edge between them. The graph is broken.

The `--json` output embeds a 384-float array per entry, making manual extraction
painful. No `--id-only` or `--ids` flag exists.

---

### Mode 5 — Unknown flag / flag ceremony

**Count: 1 explicit error (but reflects a broader confusion)**

Agent used `--confidence` and `--source` flags that do not exist. The skill or
agent's mental model of known's API included flags that never shipped.

**Evidence — session 70977423, line 137:**

First attempt:
```sh
known remember "Three feature epics opened 2026-06-08..." \
    --scope services-runtime --confidence verified --source conversation 2>&1 | tail -3
```

Tool result:
```
error: unknown flag: --confidence
```

Recovery: agent immediately retried without the flags and succeeded.

The error message names the flag but does not list valid flags or link to help.
An agent mid-task will retry, but the friction adds a round-trip and a failed
tool call.

---

### Mode 6 — Scope confusion / wrong scope in worktree

**Count: 2 incidents**

A subagent working in a git worktree used `--scope root` when the project's
`.known.yaml` has a `scope_prefix` set. The worktree's CWD is under the project
root, but the scope name `root` has no relationship to the configured prefix.

**Evidence — subagent agent-aadf07655847c115b, line 259:**

```sh
cd /home/dustin/code/personal/services-runtime/.claude/worktrees/agent-aadf07655847c115b
known remember "GI-zero bug in DeferredRenderer radiance cascades..." --scope root
```

Tool result:
```
Expires:    2026-09-03T20:03:50Z
Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)
```

The store succeeded — but under scope `root`, not `services-runtime.engine.graphics`
or whatever the `.known.yaml` specifies. The entry is likely unreachable from
project-scoped recalls.

**Evidence — subagent agent-aadf07655847c115b, line 494:**

```sh
known remember "DeferredRenderer RAII state isolation..." --scope root 2>&1 | tail -1 || true
```

Tool result:
```
(Bash completed with no output)
```

The `|| true` suppresses any error. The agent never knew if this stored or failed.

---

### Mode 7 — Plugin YAML parse failure in omp/Pi (every session)

**Count: every omp session (structural, not per-invocation)**

The known marketplace plugin (`known/0.2.0/commands/discover.md`) fails YAML
frontmatter parsing on every omp session start. The plugin is silently broken;
agents in omp cannot use `/known:` slash commands.

**Evidence — omp.2026-07-11.log:**

```json
{"timestamp":"2026-07-11T09:35:10.602-06:00","level":"warn","pid":603198,
 "message":"Failed to parse YAML frontmatter",
 "err":"Failed to parse YAML frontmatter (/home/dustin/.claude/plugins/cache/
  known-marketplace/known/0.2.0/commands/discover.md): YAML Parse error:
  Unexpected token\n..."}
```

Identical errors appear in logs from 2026-07-11, 2026-07-12, and 2026-07-17.
Zero known invocations in any omp log.

---

### Mode 8 — Recall returns stale unrelated results without warning

**Count: 2 incidents (same session, consecutive queries)**

When querying for a topic with no matching entries, `known recall` returns the
closest hits by vector similarity — including entries that are clearly off-topic.
No "no results for this query" signal distinguishes "found relevant" from
"returning the least-irrelevant thing I have".

**Evidence — session 70977423, lines 20-21:**

Query 1:
```sh
known recall 'animation system 2D sprite' 2>&1 | head -40
```

Result received by agent:
```
[services-runtime.engine.graphics.lighting: 2D lighting system]
(inferred, source: .../LightingPass.hpp, LightingManager2D.hpp, stale 81d)
{01KM1SM9A94SAWTZDJX70ZYPGN}
LightingManager2D manages point/spot/directional Light2D sources...

[services-runtime.services.game: Player entity factory]
(inferred, source: .../Player.hpp, stale 81d) {01KM1SM9A9F3XVCYXSSKASSJ52}
Player factory creates entities with full component setup...
```

The animation system did not exist in known. The agent received lighting and
player entries — stale, off-topic — with no indication that animation facts were
absent.

Query 2 (same session):
```sh
known recall 'UI UX system widgets' 2>&1 | head -40
```

Returned the same two lighting/player entries again.

---

### Mode 9 — Hook output contaminates recall result

**Count: 1 incident**

In one session, the tool result for a `known recall` call included the full
beads SESSION CLOSE PROTOCOL checklist injected by a session hook. The agent
received recall output and an unrelated 40-line checklist as one tool result.

**Evidence — session 70977423, lines 23-25:**

Tool result block (abbreviated):
```
[services-runtime.engine.graphics.lighting: 2D lighting system] ...
LightingManager2D manages point/spot/directional Light2D sources...

# Beads Workflow Context
> **Context Recovery**: Run `bd prime` after compaction...
# 🚨 SESSION CLOSE PROTOCOL 🚨
**CRITICAL**: Before saying "done" or "complete", you MUST run this checklist:
[ ] 1. git status
...
```

This is a session hook ordering issue, but the effect is that the agent's
recall result is buried in unrelated hook content. Context tokens consumed; agent
potentially confused about what was a fact vs. a protocol.

---

## Summary Table

| # | Mode | Count | Severity | Harnesses |
|---|------|-------|----------|-----------|
| 1 | Missed capture (agent never invokes known) | 52 sessions / 93% | Critical | Claude Code |
| 2 | Output buries result in embedding boilerplate | all successful stores | High | Claude Code |
| 3 | Silent success — no output visible | 2 invocations | High | Claude Code |
| 4 | ID format mismatch — link/supersede never created | 1 (blocks all updates) | High | Claude Code |
| 5 | Unknown flag / flag ceremony | 1 error + retry | Medium | Claude Code |
| 6 | Scope confusion in worktree | 2 invocations | Medium | Claude Code |
| 7 | Plugin YAML parse failure in omp | every session | High | omp/Pi |
| 8 | Recall returns stale unrelated results, no zero-hit signal | 2 queries | Medium | Claude Code |
| 9 | Hook output contaminates recall result | 1 | Low | Claude Code |

**opencode:** zero known usage found. Not installed or not surfaced.

---

## Implications for zv1.2 (capture surface) and zv1.3 (linking)

### zv1.2 — Capture surface redesign

**1. Capture must be ambient, not deliberate.**  
Mode 1 (93% miss rate) is the existential problem. The fix is not better
documentation — agents under task pressure don't stop to think "should I store
this?". Capture must happen as a side effect. Candidate directions:
- Auto-capture on session end: the harness (or a `known` subcommand) reviews
  the session diff and proposes entries for confirmation or stores them directly.
- Push model: the skill prompt instructs agents to call `known remember` at every
  "key finding" phrase, not only at session close.
- Batch capture: `known remember-batch` accepts a newline-delimited list of facts
  so one command stores multiple entries atomically.

**2. Success output must be agent-readable.**  
Modes 2 and 3 (boilerplate output, silent success) mean agents cannot confirm
capture. The `known remember` success line should be:
- A single line: `stored {ID} "{first 60 chars of content}"`
- Optionally a second line with probable links: `probably links: {ID2} ({similarity}%)`
- No embedding model line, no Expires line (those are for `--verbose`).

**3. Errors must name the fix.**  
Mode 5 (unknown flag) prints the bad flag but not the valid ones. Error output
should include: `valid flags: --scope, --link`. Mode 8 (empty recall) should
print: `no entries matched; try a broader query or check scope with known scope`.

**4. Scope must be auto-derived, not spelled out.**  
Mode 6 (scope confusion) is partially addressed by known-zv1.4, but the key
point for zv1.2: if `--scope` is omitted and `.known.yaml` is present, use its
`scope_prefix`. If neither exists, default to the git-root-derived scope. The
agent should never need to guess `--scope root` vs. `--scope engine.graphics`.

### zv1.3 — Linking / graph building

**1. Provide an `--id-only` flag on search.**  
Mode 4 (ID format mismatch) arose because the agent tried to extract an ID from
`--json` output that also contained 384-float embeddings. `known search --id-only
'query'` should print one ULID per line, nothing else. Agents can use this in
shell pipelines without parsing JSON or knowing the ID format.

**2. Supersede workflow needs a dedicated path.**  
The pattern "search for old entry → extract ID → store correction with link" is
4 steps and broke at step 2. A `known supersede 'query' "new content"` command
(or `known remember --supersedes 'query'`) would: search, confirm the match, and
create a `supersedes` edge atomically. Agents do not need to handle ULIDs.

**3. Auto-link on store (similarity suggestions).**  
After a successful store, print 1–3 probable `related-to` candidates by
embedding similarity. This turns the success confirmation (fix for Mode 2) into
a graph-building prompt: the agent can immediately run `known link {newID} --to
{suggestedID} --type related-to` if it agrees. No session-end ceremony needed.

**4. Stale entries need a recall-time warning.**  
Mode 8 (stale unrelated results): the `(stale 81d)` marker exists in output but
doesn't prevent off-topic results from filling the response. Consider: if
top-k similarity is below a threshold, print `WARNING: top result score 0.31 —
no strong match found for this query` before the results, so agents can recognize
a knowledge gap vs. a successful recall.
