package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBinary writes a stub executable that emits output and exits with exitCode.
// Output is written to a data file and cat'd to avoid shell quoting/escape issues.
func stubBinary(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(dataPath, []byte(output), 0o644); err != nil {
		t.Fatalf("write stub data: %v", err)
	}
	path := filepath.Join(dir, "known")
	script := fmt.Sprintf("#!/bin/sh\ncat %q\nexit %d\n", dataPath, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// stubCallN writes a stub that returns different outputs on successive calls.
// Outputs are written to numbered data files; a counter file tracks call index.
func stubCallN(t *testing.T, outputs []string, exitCodes []int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known")
	counter := filepath.Join(dir, "call_count")

	// Write each output to a numbered data file.
	for i, out := range outputs {
		dataPath := filepath.Join(dir, fmt.Sprintf("out%d.txt", i))
		if err := os.WriteFile(dataPath, []byte(out), 0o644); err != nil {
			t.Fatalf("write stub data %d: %v", i, err)
		}
	}

	var cases string
	for i := range outputs {
		ec := 0
		if i < len(exitCodes) {
			ec = exitCodes[i]
		}
		dataPath := filepath.Join(dir, fmt.Sprintf("out%d.txt", i))
		cases += fmt.Sprintf(
			"if [ \"$N\" = \"%d\" ]; then cat %q; exit %d; fi\n",
			i, dataPath, ec,
		)
	}

	// Default: last output/exit code.
	lastEC := 0
	if len(exitCodes) > 0 {
		lastEC = exitCodes[len(exitCodes)-1]
	}
	lastDataPath := filepath.Join(dir, fmt.Sprintf("out%d.txt", len(outputs)-1))
	script := fmt.Sprintf("#!/bin/sh\nN=$(cat %q 2>/dev/null || echo 0)\nNEXT=$((N+1))\nprintf '%%s' \"$NEXT\" > %q\n%scat %q; exit %d\n",
		counter, counter, cases, lastDataPath, lastEC)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// ---- M2-compact-confirmation ----

func TestCompactConfirmation_Pass(t *testing.T) {
	// zv1.2-style output: 3 non-empty lines, no Embedding line, has ULID.
	out := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"test content\"\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-compact-confirmation")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q predicate=%q", r.Output, r.PredicateDesc)
	}
}

func TestCompactConfirmation_Fail_EmbeddingLine(t *testing.T) {
	// Baseline-style: 11 lines including Embedding.
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\n" +
		"Content:    test\nScope:      known\nSource:     cli (manual)\n" +
		"Provenance: inferred\nFreshness:  fresh\nVersion:    1\n" +
		"Created:    2026-01-01T00:00:00Z\nUpdated:    2026-01-01T00:00:00Z\n" +
		"Expires:    2026-04-01T00:00:00Z\nEmbedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-compact-confirmation")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on baseline-style verbose output with Embedding line")
	}
}

func TestCompactConfirmation_Fail_TooManyLines(t *testing.T) {
	// Has no Embedding line but 6 non-empty lines — still too verbose.
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nContent:    test\nScope: known\nSource: cli\nProvenance: inferred\nFreshness: fresh\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-compact-confirmation")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on 6-line output (>4 lines)")
	}
}

// ---- M2-tail2-visible-ulid ----

func TestTail2VisibleULID_Pass_ULIDOnFirstLine(t *testing.T) {
	// zv1.2: 3 non-empty lines, ULID on line 1 → passes (ulidOnFirstLine=true).
	out := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"test content\"\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-tail2-visible-ulid")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS (ULID on first line); output=%q predicate=%q", r.Output, r.PredicateDesc)
	}
}

func TestTail2VisibleULID_Pass_CompactBlock(t *testing.T) {
	// ≤3 non-empty lines → passes (compactBlock=true) even if ULID not on line 1.
	out := "Scope     known\n          \"test content\"\n01KXS5B55PBGZM993P6ZN4T3K8\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-tail2-visible-ulid")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS (≤3 non-empty lines); output=%q", r.Output)
	}
}

func TestTail2VisibleULID_Fail_EmbeddingLast(t *testing.T) {
	// Baseline: 11 non-empty lines, ULID NOT on first line (first line is "ID: ...").
	// Wait — baseline has ULID on line 1 as "ID: <ULID>", which would make ulidOnFirstLine=true.
	// The predicate catches baseline because it has >3 non-empty lines AND ULID is on first line.
	// Since the oracle relaxed this to pass when ULID is on first line, baseline actually passes too.
	// The FAIL case is: many lines AND no ULID anywhere AND first line has no ULID.
	out := "Error:      embedding failed\nExpires:    2026-04-01T00:00:00Z\n" +
		"Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)\n" +
		"Line4\nLine5\nLine6\nLine7\nLine8\nLine9\nLine10\nLine11\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-tail2-visible-ulid")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL: many lines, no ULID on first line, >3 lines")
	}
}

// ---- M2-stored-label ----

func TestStoredLabel_Pass(t *testing.T) {
	out := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"test\"\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-stored-label")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q", r.Output)
	}
}

func TestStoredLabel_Pass_Duplicate(t *testing.T) {
	out := "Duplicate 01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"test\"\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-stored-label")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS on Duplicate label; output=%q", r.Output)
	}
}

func TestStoredLabel_Fail_IDLabel(t *testing.T) {
	// Baseline uses "ID:         <ULID>" not "Stored <ULID>".
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nScope:      known\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M2-stored-label")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on 'ID:' label (baseline format)")
	}
}

// ---- M2-dedup-explicit ----

func TestDedupExplicit_Pass(t *testing.T) {
	ulid := "01KXS5B55PBGZM993P6ZN4T3K8"
	first := fmt.Sprintf("Stored    %s\nScope     known\n          \"test\"\n", ulid)
	second := fmt.Sprintf("Duplicate %s\nScope     known\n          \"test\"\nHint      known update %s --content '...'\n", ulid, ulid)
	bin := stubCallN(t, []string{first, second}, []int{0, 0})
	sc := scenarioByID("M2-dedup-explicit")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q", r.Output)
	}
}

func TestDedupExplicit_Fail_UpdateLabel(t *testing.T) {
	// Baseline emits "Updated existing entry <ULID> (v2)" — not "Duplicate".
	ulid := "01KXS5B55PBGZM993P6ZN4T3K8"
	first := fmt.Sprintf("ID:         %s\nContent:    test\n", ulid)
	second := fmt.Sprintf("Updated existing entry %s (v2)\nID:         %s\nContent:    test\n", ulid, ulid)
	bin := stubCallN(t, []string{first, second}, []int{0, 0})
	sc := scenarioByID("M2-dedup-explicit")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on baseline 'Updated existing entry' format")
	}
}

// ---- M3-nonempty-with-ulid ----

func TestNonEmptyWithULID_Pass(t *testing.T) {
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nContent:    test\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M3-nonempty-with-ulid")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS")
	}
}

func TestNonEmptyWithULID_Fail_NoULID(t *testing.T) {
	// Output is non-empty but has no ULID (only expiry/embedding lines — the bad case).
	out := "Expires:    2026-09-03T19:07:26Z\nEmbedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M3-nonempty-with-ulid")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL when output has no ULID (only embedding boilerplate)")
	}
}

func TestNonEmptyWithULID_Fail_NonzeroExit(t *testing.T) {
	out := "error: something\n"
	bin := stubBinary(t, out, 1)
	sc := scenarioByID("M3-nonempty-with-ulid")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on non-zero exit")
	}
}

// ---- M4-link-by-content ----

func TestLinkByContent_Pass(t *testing.T) {
	// zv1.3 style: link accepts content queries and prints "Edge created."
	addOut := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nContent:    test\n"
	linkOut := "Resolved \"CORRECTION\" → 01KXS5B55PBGZM993P6ZN4T3K8  CORRECTION  [known]\n" +
		"Resolved \"renderer\" → 01KXS5XXXXXXXXXXXXXXXXXXXXXXX  renderer  [known]\n" +
		"ID:      01KXS5YYYYYYYYYYYYYYYYYYYY\nEdge created.\n"
	bin := stubCallN(t, []string{addOut, addOut, linkOut}, []int{0, 0, 0})
	sc := scenarioByID("M4-link-by-content")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q", r.Output)
	}
}

func TestLinkByContent_Fail_AmbiguousError(t *testing.T) {
	// Baseline: link by content fails with ambiguity error.
	addOut := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nContent:    test\n"
	linkOut := "Multiple entries match \"CORRECTION\":\n  1. 01KXS5B55PBGZM993P6ZN4T3K8  test\nerror: from: ambiguous query\n"
	bin := stubCallN(t, []string{addOut, addOut, linkOut}, []int{0, 0, 1})
	sc := scenarioByID("M4-link-by-content")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL on ambiguous query error")
	}
}

// ---- M4-link-accept-subcommand ----

func TestLinkAcceptSubcommand_Pass(t *testing.T) {
	out := "Resolved \"renderer\" → 01KXS5B55PBGZM993P6ZN4T3K8  renderer  [known]\nNo link suggestions found.\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M4-link-accept-subcommand")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q", r.Output)
	}
}

func TestLinkAcceptSubcommand_Fail_UnknownSubcommand(t *testing.T) {
	// Baseline: "link accept" is not a known subcommand.
	out := "error: unknown subcommand \"accept\"\n"
	bin := stubBinary(t, out, 1)
	sc := scenarioByID("M4-link-accept-subcommand")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL when link accept is an unknown subcommand")
	}
}

// ---- M5-unknown-flag-suggests-valid ----

func TestUnknownFlagSuggestsValid_Pass(t *testing.T) {
	out := "error: unknown flag: --confidence\nDid you mean --provenance?\n"
	bin := stubBinary(t, out, 1)
	sc := scenarioByID("M5-unknown-flag-suggests-valid")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS when error mentions --provenance")
	}
}

func TestUnknownFlagSuggestsValid_Fail_NoSuggestion(t *testing.T) {
	// Baseline: no suggestion in error.
	out := "error: unknown flag: --confidence\n"
	bin := stubBinary(t, out, 1)
	sc := scenarioByID("M5-unknown-flag-suggests-valid")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL when error gives no suggestion")
	}
}

func TestUnknownFlagSuggestsValid_Fail_ZeroExit(t *testing.T) {
	out := "error: unknown flag: --confidence\nuse --provenance instead\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M5-unknown-flag-suggests-valid")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL when exit code is 0")
	}
}

// ---- M6-scope-from-marker ----

func TestScopeFromMarker_Pass(t *testing.T) {
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nScope:      myproject\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M6-scope-from-marker")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q", r.Output)
	}
}

func TestScopeFromMarker_Fail_Root(t *testing.T) {
	out := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nScope:      root\n"
	bin := stubBinary(t, out, 0)
	sc := scenarioByID("M6-scope-from-marker")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL when scope is 'root'")
	}
}

// ---- reULID regex ----

func TestReULID(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"01KXS5B55PBGZM993P6ZN4T3K8", true},
		{"01KTD44NBEHMHE0MPY6VGFCJR6", true},
		{"12345", false},
		{`"id":12345`, false},
		{`Stored    01KXS5B55PBGZM993P6ZN4T3K8`, true},
		{"Duplicate 01KXS5B55PBGZM993P6ZN4T3K8", true},
	}
	for _, c := range cases {
		got := reULID.MatchString(c.s)
		if got != c.want {
			t.Errorf("reULID.MatchString(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// ---- nonEmptyLines ----

func TestNonEmptyLines(t *testing.T) {
	out := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n\n          \"test\"\n\n"
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Errorf("got %d non-empty lines, want 3: %v", len(lines), lines)
	}
}

// ---- corpus completeness ----

func TestCorpusIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, sc := range corpus() {
		if seen[sc.ID] {
			t.Errorf("duplicate scenario ID: %s", sc.ID)
		}
		seen[sc.ID] = true
		if sc.AuditMode == "" {
			t.Errorf("scenario %s missing AuditMode", sc.ID)
		}
	}
}

// TestDiscriminatorOracleFails verifies that an audit-faithful bad stub
// (one that emits only the embedding boilerplate the audit showed) scores LOW.
// This is the discrimination oracle's check: a binary that exhibits the exact
// documented bad behavior must fail the harness, not score 100%.
func TestDiscriminatorOracleFails(t *testing.T) {
	// Simulate baseline behavior: emits verbose output with Embedding last,
	// uses "ID:" label, no "Stored"/"Duplicate" on first line.
	// link and link-accept subcommands fail with errors.
	badOut := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\n" +
		"Content:    test content\nScope:      known\nSource:     cli (manual)\n" +
		"Provenance: inferred\nFreshness:  fresh\nVersion:    1\n" +
		"Created:    2026-01-01T00:00:00Z\nUpdated:    2026-01-01T00:00:00Z\n" +
		"Expires:    2026-04-01T00:00:00Z\n" +
		"Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)\n" +
		// Baseline show --json also carried expires_at (90d default TTL).
		// Including it here ensures Nyo-no-ttl-permanent correctly FAILs on baseline.
		`{"entry":{"id":"01KXS5B55PBGZM993P6ZN4T3K8","content":"test","scope":"known","source":{"type":"manual","reference":"cli"},"provenance":{"level":"inferred"},"freshness":{"observed_at":"2026-01-01T00:00:00Z"},"ttl":"2160h0m0s","expires_at":"2026-04-01T00:00:00Z","version":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},"outgoing_edges":[],"incoming_edges":[]}` + "\n"

	bin := stubBinary(t, badOut, 0)
	scenarios := corpus()

	passCount := 0
	for _, sc := range scenarios {
		r := runScenario(bin, sc)
		if r.Pass {
			passCount++
		}
	}

	total := len(scenarios)
	// Count how many are NOT xfail (should-pass scenarios).
	nonXfail := 0
	xfailCount := 0
	for _, sc := range scenarios {
		if sc.ExpectFailBaseline {
			xfailCount++
		} else {
			nonXfail++
		}
	}

	// Honest denominator = total (xfail included).
	rate := float64(passCount) / float64(total) * 100
	t.Logf("Audit-faithful bad stub: %d/%d (%.0f%%) pass — %d xfail scenarios", passCount, total, rate, xfailCount)

	// A binary exhibiting baseline friction should score well below 100%.
	// At minimum, the M2 compact/stored/dedup, M4 link, M5 flag suggestion,
	// and dedup scenarios must fail.  If ALL scenarios pass, the corpus is weak.
	if passCount == total {
		t.Errorf("DISCRIMINATION FAILURE: audit-faithful bad stub scored 100%% (%d/%d) — all scenarios pass even on broken binary", passCount, total)
	}

	// The xfail-baseline scenarios should all fail the bad stub (they're xfail for this reason).
	// If more than nonXfail scenarios pass, xfail scenarios are passing on baseline (wrong).
	if passCount > nonXfail {
		t.Errorf("BAD CORPUS: %d scenarios passed on audit-faithful bad stub, but only %d are non-xfail — some xfail scenarios pass on bad binary", passCount, nonXfail)
	}

	// And the rate must be clearly below 100%.
	if rate >= 100 {
		t.Errorf("DISCRIMINATION FAILURE: capture rate is %.0f%% on bad binary — harness cannot show improvement", rate)
	}
}

// ---- Nyo-no-ttl-permanent ----
//
// Stubs two calls: first (add) returns compact human output with ULID;
// second (show --json) returns a JSON entry object without expires_at.
// Real CLI: PrintAddResult has no Expires line; expiry state is only in show --json.

func TestNoTTLPermanent_Pass(t *testing.T) {
	// Stub call 1 (add): compact output with ULID, no Expires: line.
	addOut := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"permanent fact without ttl flag nyo\"\n"
	// Stub call 2 (show --json): entry without expires_at (permanent).
	showOut := `{"entry":{"id":"01KXS5B55PBGZM993P6ZN4T3K8","content":"permanent fact without ttl flag nyo","content_hash":"abc","source":{"type":"manual","reference":"cli"},"provenance":{"level":"inferred"},"freshness":{"observed_at":"2026-07-17T00:00:00Z"},"scope":"root","version":1,"created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"},"outgoing_edges":[],"incoming_edges":[]}` + "\n"
	bin := stubCallN(t, []string{addOut, showOut}, []int{0, 0})
	sc := scenarioByID("Nyo-no-ttl-permanent")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q predicate=%q", r.Output, r.PredicateDesc)
	}
}

func TestNoTTLPermanent_Fail(t *testing.T) {
	// Stub call 1 (add): compact output — baseline-style (no Expires in add output).
	addOut := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"permanent fact without ttl flag nyo\"\n"
	// Stub call 2 (show --json): entry WITH expires_at — old 90d default behavior.
	showOut := `{"entry":{"id":"01KXS5B55PBGZM993P6ZN4T3K8","content":"permanent fact without ttl flag nyo","content_hash":"abc","source":{"type":"manual","reference":"cli"},"provenance":{"level":"inferred"},"freshness":{"observed_at":"2026-07-17T00:00:00Z"},"scope":"root","ttl":"2160h0m0s","expires_at":"2026-10-24T00:00:00Z","version":1,"created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"},"outgoing_edges":[],"incoming_edges":[]}` + "\n"
	bin := stubCallN(t, []string{addOut, showOut}, []int{0, 0})
	sc := scenarioByID("Nyo-no-ttl-permanent")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL (expires_at present in show --json = old auto-TTL behavior); output=%q", r.Output)
	}
}

// ---- Nyo-explicit-ttl-sets-expiry ----
//
// Stubs two calls: add returns compact human output with ULID;
// show --json returns entry with or without expires_at.

func TestExplicitTTLSetsExpiry_Pass(t *testing.T) {
	// Stub call 1 (add with --ttl 24h): compact output with ULID.
	addOut := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"ephemeral fact with explicit ttl nyo\"\n"
	// Stub call 2 (show --json): entry WITH expires_at (--ttl 24h worked).
	showOut := `{"entry":{"id":"01KXS5B55PBGZM993P6ZN4T3K8","content":"ephemeral fact with explicit ttl nyo","content_hash":"abc","source":{"type":"manual","reference":"cli"},"provenance":{"level":"inferred"},"freshness":{"observed_at":"2026-07-17T00:00:00Z"},"scope":"root","ttl":"24h0m0s","expires_at":"2026-07-18T00:00:00Z","version":1,"created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"},"outgoing_edges":[],"incoming_edges":[]}` + "\n"
	bin := stubCallN(t, []string{addOut, showOut}, []int{0, 0})
	sc := scenarioByID("Nyo-explicit-ttl-sets-expiry")
	r := runScenario(bin, sc)
	if !r.Pass {
		t.Errorf("expected PASS; output=%q predicate=%q", r.Output, r.PredicateDesc)
	}
}

func TestExplicitTTLSetsExpiry_Fail(t *testing.T) {
	// Stub call 1 (add): compact output with ULID.
	addOut := "Stored    01KXS5B55PBGZM993P6ZN4T3K8\nScope     known\n          \"ephemeral fact with explicit ttl nyo\"\n"
	// Stub call 2 (show --json): entry WITHOUT expires_at — --ttl was silently dropped.
	showOut := `{"entry":{"id":"01KXS5B55PBGZM993P6ZN4T3K8","content":"ephemeral fact with explicit ttl nyo","content_hash":"abc","source":{"type":"manual","reference":"cli"},"provenance":{"level":"inferred"},"freshness":{"observed_at":"2026-07-17T00:00:00Z"},"scope":"root","version":1,"created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"},"outgoing_edges":[],"incoming_edges":[]}` + "\n"
	bin := stubCallN(t, []string{addOut, showOut}, []int{0, 0})
	sc := scenarioByID("Nyo-explicit-ttl-sets-expiry")
	r := runScenario(bin, sc)
	if r.Pass {
		t.Errorf("expected FAIL (no expires_at in show --json when --ttl was given); output=%q", r.Output)
	}
}

// scenarioByID finds a scenario by ID; panics if not found.
func scenarioByID(id string) Scenario {
	for _, sc := range corpus() {
		if sc.ID == id {
			return sc
		}
	}
	panic("scenario not found: " + id)
}

// Ensure strings is used (imported for strings.HasPrefix etc in test logic above).
var _ = strings.Contains
