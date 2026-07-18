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
	// ID is a short unique identifier (kept stable across runs).
	ID string
	// Name is a human-readable label.
	Name string
	// AuditMode cites the friction-audit section this scenario exercises.
	AuditMode string
	// ExpectFailBaseline marks scenarios whose contract is not met by either
	// the pre-wave-2 baseline (a59396f) or the current epic branch.
	// These are scored XFAIL (expected failure) rather than FAIL.
	ExpectFailBaseline bool
	// Run executes the scenario against the given binary and returns the
	// combined stdout+stderr output, exit code, predicate description, and pass bool.
	Run func(bin string) (output string, exitCode int, predicateDesc string, pass bool)
}

// sharedModelDir is set once by main before running scenarios.
// It is the ~/.known/models directory from the real HOME; we symlink it into
// each scenario's temp HOME so the embedder finds the cached model directory.
// Note: hugot still contacts Hugging Face on each run regardless of the symlink;
// network access is required and repeated runs may be rate-limited.
var sharedModelDir string

// corpus returns the full scenario set derived from docs/friction-audit.md.
//
// Design intent: every scenario that tests friction fixed by zv1.2/zv1.3 MUST
// fail on the baseline binary (ExpectFailBaseline=true) so that the capture-rate
// metric can show before/after improvement.  Scenarios that test already-landed
// features are regression guards (ExpectFailBaseline=false, always pass).
func corpus() []Scenario {
	return []Scenario{

		// ---- Mode 2: Output buries result — compact confirmation block ----
		//
		// Audit incident (c1f50adc:110,436): agent piped `known remember ... 2>&1 | tail -2`
		// and saw only `Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)` —
		// no ULID, no scope, no confirmation.
		//
		// Contract: output must be compact (≤4 non-empty lines) with no embedding boilerplate.
		// Baseline fails this (11 lines including Embedding).
		// zv1.2's 3-line confirmation block passes (Stored/Scope/content, no Embedding).
		{
			ID:                 "M2-compact-confirmation",
			Name:               "add: confirmation block is compact (≤4 non-empty lines, no embedding line)",
			AuditMode:          "Mode 2 — Output buries result in embedding boilerplate (c1f50adc:110,436)",
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "DeferredRenderer distance field target for engine.graphics")
				if code != 0 {
					return out, code, "exit 0", false
				}
				lines := nonEmptyLines(out)
				hasULID := reULID.MatchString(out)
				noEmbedLine := !strings.Contains(out, "Embedding:")
				compact := len(lines) <= 4
				pass := hasULID && noEmbedLine && compact
				return out, code, fmt.Sprintf("contains ULID AND no 'Embedding:' line AND ≤4 non-empty lines (got %d)", len(lines)), pass
			},
		},

		// ---- Mode 2: ULID on first line or compact block (regression guard) ----
		//
		// Oracle ruling: the original "tail -2 shows ULID" predicate was over-literal.
		// Mode 2 is resolved by M2-compact-confirmation; this scenario validates that
		// the 'remember' alias also emits a ULID on the first line (or ≤3 lines total).
		// Relaxed predicate passes both baseline and epic — regression guard only.
		{
			ID:        "M2-tail2-visible-ulid",
			Name:      "remember: ULID on first output line or output ≤3 non-empty lines",
			AuditMode: "Mode 2 — Output buries result (c1f50adc:436); predicate relaxed per oracle ruling",
			// Not ExpectFailBaseline: relaxed predicate passes baseline (ID: on line 1 has ULID).
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "remember", "Renderer architecture decision 2026-06 SIBLING RendererInterface")
				if code != 0 {
					return out, code, "exit 0", false
				}
				lines := nonEmptyLines(out)
				ulidOnFirstLine := len(lines) > 0 && reULID.MatchString(lines[0])
				compactBlock := len(lines) <= 3
				pass := ulidOnFirstLine || compactBlock
				return out, code, fmt.Sprintf("ULID on first line (%v) OR ≤3 non-empty lines (got %d)", ulidOnFirstLine, len(lines)), pass
			},
		},

		// ---- Mode 2: Confirmation block surfaces ULID+scope (zv1.2 contract) ----
		//
		// zv1.2 contract: first line = "Stored <ULID>" or "Duplicate <ULID>".
		// Baseline uses "ID:         <ULID>" — a different label format.
		{
			ID:                 "M2-stored-label",
			Name:               "add: first output line uses 'Stored'/'Duplicate' label (not 'ID:')",
			AuditMode:          "Mode 2 — zv1.2 confirmation block contract",
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "radiance cascades GI-zero bug DeferredRenderer")
				if code != 0 {
					return out, code, "exit 0", false
				}
				first := firstLine(out)
				pass := reULID.MatchString(first) &&
					(strings.HasPrefix(first, "Stored") || strings.HasPrefix(first, "Duplicate"))
				return out, code, "first stdout line starts with 'Stored'/'Duplicate' and contains a ULID", pass
			},
		},

		// ---- Mode 2: Dedup outcome — explicit "Duplicate <ULID>" ----
		//
		// Audit (70977423:137-139): no dedup signal was ever observed.
		// zv1.2 contract: second identical add must emit "Duplicate <ULID>" on line 1.
		// Baseline emits "Updated existing entry <ULID> (v2)".
		{
			ID:                 "M2-dedup-explicit",
			Name:               "add (dedup): second identical add prints 'Duplicate <ULID>' on first line",
			AuditMode:          "Mode 2 — Dedup note (70977423:137-139); zv1.2 dedup surface",
			ExpectFailBaseline: true,
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
				secondFirst := firstLine(second)
				pass := strings.HasPrefix(secondFirst, "Duplicate") && strings.Contains(secondFirst, ulid)
				return second, 0, "first line of second add starts with 'Duplicate' and contains original ULID", pass
			},
		},

		// ---- Mode 3: Silent success (stdout non-empty with ULID) ----
		//
		// Audit (c1f50adc:1034,1427): bash tool returned "(Bash completed with no output)".
		// Kept as regression guard (baseline already passes).
		{
			ID:        "M3-nonempty-with-ulid",
			Name:      "add: stdout is non-empty and contains a ULID on success",
			AuditMode: "Mode 3 — Silent success (c1f50adc:1034,1427)",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add", "radiance cascades DeferredRenderer GI")
				pass := code == 0 && reULID.MatchString(out)
				return out, code, "exit 0 AND stdout contains a ULID", pass
			},
		},

		// ---- Mode 4: Link by content (no ULIDs required) ----
		//
		// Audit (c1f50adc:1057): agent extracted ID with `grep -o '"id":[0-9]*'`,
		// got empty (ULID doesn't match), stored correction with no supersedes edge.
		// zv1.3 contract: `known link "<from-content>" "<to-content>" --type supersedes`
		// resolves both entries by EXACT content and creates the edge without any ULID.
		//
		// Queries use the exact stored content strings so the resolver's
		// exact-match path fires (avoids ambiguity when vocabulary overlaps).
		// Baseline fails: no confident content resolution → ambiguity error.
		{
			ID:                 "M4-link-by-content",
			Name:               "link: supersedes edge created by exact content query, no ULIDs typed",
			AuditMode:          "Mode 4 — ID format mismatch / link never created (c1f50adc:1057); zv1.3 contract",
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				const (
					fromContent = "CORRECTION to renderer decoupling decision"
					toContent   = "renderer architecture decision original SIBLING RendererInterface"
				)
				run(bin, env, dir, "add", toContent)
				run(bin, env, dir, "add", fromContent)
				out, code := runArgs(bin, env, dir, []string{
					"link", fromContent, toContent, "--type", "supersedes",
				})
				pass := code == 0 && strings.Contains(out, "Edge created.")
				return out, code, "exit 0 AND stdout contains 'Edge created.'", pass
			},
		},

		// ---- Mode 4: link accept — suggestion-driven edge creation ----
		//
		// zv1.3 contract: `known link accept "<entry-query>" --all` accepts link
		// suggestions without any ULID input.
		// Baseline: no `link accept` subcommand (exits 1).
		{
			ID:                 "M4-link-accept-subcommand",
			Name:               "link accept: subcommand exists and accepts an entry query",
			AuditMode:          "Mode 4 — link-accept surface (zv1.3); link workflow w/o ULIDs",
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				run(bin, env, dir, "add", "renderer architecture decision original baseline")
				run(bin, env, dir, "add", "radiance cascade related entry for suggestion")
				out, code := runArgs(bin, env, dir, []string{
					"link", "accept", "renderer architecture decision original", "--all",
				})
				pass := code == 0
				return out, code, "link accept subcommand exits 0", pass
			},
		},

		// ---- Mode 5: Unknown flag error names a valid alternative ----
		//
		// Audit (70977423:137): `--confidence` rejected with "unknown flag: --confidence",
		// no valid alternatives listed.
		// zv1.2 contract: error message must suggest the nearest valid flag.
		// Baseline: only says "unknown flag: --confidence".
		{
			ID:                 "M5-unknown-flag-suggests-valid",
			Name:               "unknown flag --confidence: error message names a valid alternative",
			AuditMode:          "Mode 5 — Unknown flag / flag ceremony (70977423:137); zv1.2 contract",
			ExpectFailBaseline: true,
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				out, code := run(bin, env, dir, "add",
					"Three feature epics opened 2026-06-08",
					"--confidence", "verified")
				hasSuggestion := strings.Contains(out, "--provenance") ||
					strings.Contains(out, "--scope") ||
					strings.Contains(out, "--ttl") ||
					strings.Contains(out, "--source") ||
					strings.Contains(out, "Did you mean") ||
					strings.Contains(out, "valid flags") ||
					strings.Contains(out, "help")
				pass := code != 0 && hasSuggestion
				return out, code, "exit non-zero AND output names a valid flag / 'Did you mean'", pass
			},
		},

		// ---- Mode 6: Scope from .known.yaml marker (regression guards) ----
		//
		// zv1.4 landed in the epic branch. Both scenarios pass on baseline a59396f.
		{
			ID:        "M6-scope-from-marker",
			Name:      "scope derived from .known.yaml scope_prefix in CWD",
			AuditMode: "Mode 6 — Scope confusion / zv1.4 marker derivation",
			Run: func(bin string) (string, int, string, bool) {
				env, dir, cleanup := isolatedEnv()
				defer cleanup()
				if err := os.WriteFile(filepath.Join(dir, ".known.yaml"), []byte("scope_prefix: myproject\n"), 0o644); err != nil {
					return fmt.Sprintf("setup error: %v", err), -1, "write .known.yaml", false
				}
				out, code := run(bin, env, dir, "add", "GI-zero bug in DeferredRenderer radiance cascades")
				hasPrefix := strings.Contains(out, "myproject")
				notRoot := !strings.Contains(out, "Scope:      root") && !strings.Contains(out, "Scope     root")
				pass := code == 0 && hasPrefix && notRoot
				return out, code, "Scope line contains 'myproject' and is not 'root'", pass
			},
		},
		{
			ID:        "M6-scope-subdir-appended",
			Name:      "scope appends CWD-relative subdir path to prefix",
			AuditMode: "Mode 6 — zv1.4 marker derivation (subdir variant)",
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
				pass := code == 0 && strings.Contains(out, "proj")
				return out, code, "Scope contains 'proj' prefix", pass
			},
		},
	}
}

// isolatedEnv returns env vars, a working dir, and a cleanup fn for one scenario.
// HOME is isolated to a fresh temp dir; KNOWN_DSN is a fresh temp SQLite file.
// The real HuggingFace model cache is symlinked into the temp HOME so the
// embedder finds the cached model files, but hugot still contacts Hugging Face;
// network access is required.
func isolatedEnv() (env []string, dir string, cleanup func()) {
	tmp, err := os.MkdirTemp("", "known-capture-*")
	if err != nil {
		panic(err)
	}
	home := filepath.Join(tmp, "home")
	_ = os.MkdirAll(home, 0o755)

	// Symlink the shared model cache into the temp HOME so the embedder
	// finds the cached files without writing to the real HOME.
	if sharedModelDir != "" {
		knownDir := filepath.Join(home, ".known")
		_ = os.MkdirAll(knownDir, 0o755)
		modelLink := filepath.Join(knownDir, "models")
		_ = os.Symlink(sharedModelDir, modelLink)
	}

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

// run invokes `bin <subcmd> <content> [extra...]` and returns combined output + exit code.
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

// nonEmptyLines returns the non-empty, non-whitespace-only lines from s.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
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
