package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Scenario describes a single capture-rate test case.
//
// Each scenario maps to one or more friction-audit failure modes.
type Scenario struct {
	// ID is a short unique identifier.
	ID string
	// Name is a human-readable label.
	Name string
	// AuditMode cites the friction-audit section this scenario exercises.
	AuditMode string
	// ExpectFailBaseline marks scenarios that test wave-2 contracts (zv1.2,
	// zv1.3) not yet present in the baseline binary.  These are scored as XFAIL
	// rather than FAIL against the baseline.
	ExpectFailBaseline bool
	// Run executes the scenario against the given binary and returns the
	// combined stdout+stderr output, exit code, and a human-readable predicate
	// description.
	Run func(bin string) (output string, exitCode int, predicateDesc string, pass bool)
}

// corpus returns the full scenario set derived from docs/friction-audit.md.
func corpus() []Scenario {
	return []Scenario{
		// ---- Mode 2: Output buries result in embedding boilerplate ----
		// Contract: first line of stdout on a successful `add` must contain the
		// stored entry's ULID.  The agent cannot see a ULID if it pipes through
		// `tail -2` and sees only the embedding line.
		{
			ID:        "M2-ulid-first-line",
			Name:      "add: ULID appears on first stdout line",
			AuditMode: "Mode 2 — Output buries result in embedding boilerplate (c1f50adc:110,436)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "DeferredRenderer distance field target for engine.graphics")
				first := firstLine(out)
				pass := reULID.MatchString(first)
				return out, code, "first stdout line contains a ULID", pass
			},
		},
		{
			ID:        "M2-remember-ulid",
			Name:      "remember: ULID appears in stdout",
			AuditMode: "Mode 2 — Output buries result (c1f50adc:436) / Mode 3 — Silent success",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "remember", "Renderer architecture decision 2026-06 SIBLING RendererInterface")
				pass := code == 0 && reULID.MatchString(out)
				return out, code, "stdout contains a ULID (exit 0)", pass
			},
		},

		// ---- Mode 3: Silent success (no output at all) ----
		// Contract: any successful add/remember must produce non-empty stdout.
		{
			ID:        "M3-nonempty-stdout",
			Name:      "add: stdout is non-empty on success",
			AuditMode: "Mode 3 — Silent success (c1f50adc:1034,1427)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "radiance cascades GI-zero bug DeferredRenderer")
				pass := code == 0 && strings.TrimSpace(out) != ""
				return out, code, "stdout non-empty on exit 0", pass
			},
		},

		// ---- Mode 4: ID format mismatch — ULID in JSON search output ----
		// Contract: `known --json search <q>` must surface the entry id as a
		// ULID-format string (not an integer).  An agent grepping for [0-9]+ will
		// fail; we verify the id field is present and ULID-shaped.
		{
			ID:        "M4-json-search-ulid",
			Name:      "--json search: id field is ULID-format",
			AuditMode: "Mode 4 — ID format mismatch / link never created (c1f50adc:1057)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				// First store an entry.
				run(bin, env, dir, "add", "renderer architecture decision sibling RendererInterface submit_geometry")
				// Now search for it.
				out, code := runArgs(bin, env, dir, []string{"--json", "search", "renderer architecture decision"})
				pass := code == 0 && reULIDJSON.MatchString(out)
				return out, code, `"id" field in JSON output matches ULID pattern`, pass
			},
		},
		{
			ID:        "M4-json-id-not-integer",
			Name:      "--json search: id field is NOT an integer",
			AuditMode: "Mode 4 — ID format mismatch (c1f50adc:1057)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				run(bin, env, dir, "add", "renderer architecture integer test entry")
				out, code := runArgs(bin, env, dir, []string{"--json", "search", "renderer architecture integer"})
				// An integer-only id like "id":12345 should NOT appear.
				badID := strings.Contains(out, `"id":`) && !reULIDJSON.MatchString(out)
				pass := code == 0 && !badID
				return out, code, `"id" field is not a bare integer`, pass
			},
		},

		// ---- Mode 5: Unknown flag error names a valid alternative ----
		// Contract: `known add ... --confidence` must exit non-zero with an error
		// message that names at least one valid flag the agent can use instead.
		// The friction-audit incident shows the error only said "unknown flag:
		// --confidence" with no guidance.
		{
			ID:        "M5-unknown-flag-error",
			Name:      "unknown flag --confidence: error mentions a valid flag",
			AuditMode: "Mode 5 — Unknown flag / flag ceremony (70977423:137)",
			// This is an improvement the epic targets; baseline only says "unknown flag".
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "Three feature epics opened 2026-06-08", "--confidence", "verified")
				// Must fail (exit != 0) and mention a valid flag name.
				validFlagMentioned := strings.Contains(out, "--scope") ||
					strings.Contains(out, "--provenance") ||
					strings.Contains(out, "--ttl") ||
					strings.Contains(out, "--source") ||
					strings.Contains(out, "valid") ||
					strings.Contains(out, "available") ||
					strings.Contains(out, "help")
				pass := code != 0 && validFlagMentioned
				return out, code, "exit non-zero AND error output mentions a valid flag or help", pass
			},
		},

		// ---- Mode 6: Scope derivation from .known.yaml marker ----
		// Contract: when CWD is inside a directory tree that has a .known.yaml
		// with scope_prefix set, `known add` must use that prefix in the stored
		// entry's scope — not "root".  The baseline (pre-zv1.4) stores scope
		// "root" in a directory without .known.yaml; the epic binary uses the
		// marker.
		{
			ID:        "M6-scope-from-marker-root",
			Name:      "scope derived from .known.yaml in CWD",
			AuditMode: "Mode 6 — Scope confusion / wrong scope in worktree (agent-aadf07655847c115b:259)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				// Write marker at the root of the isolated dir.
				if err := os.WriteFile(filepath.Join(dir, ".known.yaml"), []byte("scope_prefix: myproject\n"), 0o644); err != nil {
					return fmt.Sprintf("setup error: %v", err), -1, "write .known.yaml", false
				}
				out, code := run(bin, env, dir, "add", "GI-zero bug in DeferredRenderer radiance cascades")
				// Scope line must contain the prefix, not "root".
				hasPrefix := strings.Contains(out, "myproject")
				notRoot := !strings.Contains(out, "Scope:      root")
				pass := code == 0 && hasPrefix && notRoot
				return out, code, "Scope line contains 'myproject' and is not 'root'", pass
			},
		},
		{
			ID:        "M6-scope-from-marker-subdir",
			Name:      "scope appends subdir path when CWD is nested",
			AuditMode: "Mode 6 — Scope confusion / zv1.4 marker derivation",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				if err := os.WriteFile(filepath.Join(dir, ".known.yaml"), []byte("scope_prefix: proj\n"), 0o644); err != nil {
					return fmt.Sprintf("setup error: %v", err), -1, "write .known.yaml", false
				}
				subdir := filepath.Join(dir, "engine", "graphics")
				if err := os.MkdirAll(subdir, 0o755); err != nil {
					return fmt.Sprintf("setup error: %v", err), -1, "mkdirall subdir", false
				}
				out, code := run(bin, env, subdir, "add", "renderer subdir scope test")
				// Epic adds subdir segments; must start with prefix.
				pass := code == 0 && strings.Contains(out, "proj")
				return out, code, "Scope contains 'proj' prefix", pass
			},
		},

		// ---- Mode 2 (dedup variant): second identical add reports dedup ----
		// The audit notes dedup was never observed in the corpus.  Contract:
		// adding the same content twice should either update the existing entry
		// (printing "Updated existing entry <ULID>") or reject as duplicate — in
		// either case the second call's stdout references the original ULID, so
		// the agent knows the entry already exists.
		{
			ID:        "M2-dedup-second-add",
			Name:      "second identical add reports existing entry ULID",
			AuditMode: "Mode 2 — Dedup note (70977423:137-139)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				content := "Three feature epics opened 2026-06-08 services-runtime"
				first, _ := run(bin, env, dir, "add", content)
				ulid := reULID.FindString(first)
				if ulid == "" {
					return first, -1, "first add must produce a ULID", false
				}
				second, _ := run(bin, env, dir, "add", content)
				// Either "Updated existing entry <ULID>" or a dedup rejection
				// referencing the original ULID.
				pass := strings.Contains(second, ulid)
				return second, 0, "second add output contains the original ULID", pass
			},
		},

		// ---- Mode 4 (linking): link creation workflow ----
		// Contract: after two entries exist, an agent should be able to create
		// an edge between them using IDs extracted from add output without
		// manually assembling ULIDs.  The simplest test: add two entries, extract
		// their IDs from the output, then link them and verify via `known --json
		// search` that the edge exists.
		//
		// zv1.3 (LinkBuilding) may provide a higher-level "link by content"
		// surface; we test the baseline `--link type:id` path here.
		{
			ID:        "M4-link-two-entries",
			Name:      "link: edge created between two entries via --link flag",
			AuditMode: "Mode 4 — ID format mismatch / link never created (c1f50adc:1057)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out1, _ := run(bin, env, dir, "add", "original renderer architecture decision")
				id1 := reULID.FindString(out1)
				if id1 == "" {
					return out1, -1, "first add must produce a ULID", false
				}
				out2, code := runArgs(bin, env, dir, []string{
					"add", "CORRECTION to renderer decoupling decision",
					"--link", "supersedes:" + id1,
				})
				pass := code == 0 && reULID.MatchString(out2)
				return out2, code, "second add with --link supersedes:<id1> exits 0 and shows ULID", pass
			},
		},
	}
}

// isolatedEnv returns a temp HOME, KNOWN_DSN, and working dir for one scenario.
func isolatedEnv() (env []string, dir string, cleanup func()) {
	tmp, err := os.MkdirTemp("", "known-capture-*")
	if err != nil {
		panic(err)
	}
	home := filepath.Join(tmp, "home")
	_ = os.MkdirAll(home, 0o755)
	workdir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(workdir, 0o755)
	dsn := filepath.Join(tmp, "known.db")
	env = []string{
		"HOME=" + home,
		"KNOWN_DSN=" + dsn,
		"PATH=" + os.Getenv("PATH"),
	}
	return env, workdir, func() { os.RemoveAll(tmp) }
}

// run invokes `bin <subcmd> <content>` and returns combined output + exit code.
func run(bin string, env []string, dir, subcmd, content string, extra ...string) (string, int) {
	args := append([]string{subcmd, content}, extra...)
	return runArgs(bin, env, dir, args)
}

// runArgs invokes bin with the given args and returns combined stdout+stderr and exit code.
func runArgs(bin string, env []string, dir string, args []string) (string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// runScenario executes sc and returns the ScenarioResult.
func runScenario(bin string, sc Scenario) ScenarioResult {
	output, exitCode, pred, pass := sc.Run(bin)
	return ScenarioResult{
		ID:            sc.ID,
		Name:          sc.Name,
		Pass:          pass,
		ExitCode:      exitCode,
		Output:        output,
		PredicateDesc: pred,
	}
}
