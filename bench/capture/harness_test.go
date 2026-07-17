package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBinary writes a shell script that emits canned output and returns it as
// a path to a temporary executable.  The script exits with exitCode and prints
// output on stdout.
func stubBinary(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\nexit %d\n", output, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// stubBinaryWithArgs writes a stub that emits different output based on
// the first two arguments.
func stubBinaryWithArgs(t *testing.T, cases map[string]stubCase) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known")
	var body string
	body = "#!/bin/sh\nCMD=\"$1 $2\"\n"
	for key, c := range cases {
		body += fmt.Sprintf("if [ \"$CMD\" = %q ]; then printf '%%s' %q; exit %d; fi\n",
			key, c.output, c.exitCode)
	}
	// default: print args and exit 0
	body += `printf '%s' "$*"; exit 0` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

type stubCase struct {
	output   string
	exitCode int
}

// TestPredicateULIDFirstLine verifies the M2-ulid-first-line predicate.
func TestPredicateULIDFirstLine(t *testing.T) {
	goodOutput := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nContent:    test\nEmbedding:  model (384 dims)\n"
	badOutput := "Embedding:  sentence-transformers/all-MiniLM-L6-v2 (384 dims)\n"

	t.Run("pass", func(t *testing.T) {
		bin := stubBinary(t, goodOutput, 0)
		sc := scenarioByID("M2-ulid-first-line")
		r := runScenario(bin, sc)
		if !r.Pass {
			t.Errorf("expected PASS, got FAIL; output: %q", r.Output)
		}
	})
	t.Run("fail", func(t *testing.T) {
		bin := stubBinary(t, badOutput, 0)
		sc := scenarioByID("M2-ulid-first-line")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL, got PASS; output: %q", r.Output)
		}
	})
}

// TestPredicateNonEmptyStdout verifies the M3 predicate.
func TestPredicateNonEmptyStdout(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		bin := stubBinary(t, "ID:         01KXS5B55PBGZM993P6ZN4T3K8\n", 0)
		sc := scenarioByID("M3-nonempty-stdout")
		r := runScenario(bin, sc)
		if !r.Pass {
			t.Errorf("expected PASS; output=%q", r.Output)
		}
	})
	t.Run("fail-empty", func(t *testing.T) {
		bin := stubBinary(t, "", 0)
		sc := scenarioByID("M3-nonempty-stdout")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL on empty output")
		}
	})
	t.Run("fail-nonzero-exit", func(t *testing.T) {
		bin := stubBinary(t, "error: something\n", 1)
		sc := scenarioByID("M3-nonempty-stdout")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL on non-zero exit")
		}
	})
}

// TestPredicateJSONSearchULID verifies the M4 json search predicate.
func TestPredicateJSONSearchULID(t *testing.T) {
	ulid := "01KXS5B55PBGZM993P6ZN4T3K8"
	goodJSON := fmt.Sprintf(`[{"entry":{"id":%q,"content":"test"}}]`, ulid)
	badJSON := `[{"entry":{"id":12345,"content":"test"}}]`

	t.Run("pass", func(t *testing.T) {
		bin := stubBinary(t, goodJSON, 0)
		sc := scenarioByID("M4-json-search-ulid")
		r := runScenario(bin, sc)
		if !r.Pass {
			t.Errorf("expected PASS; output=%q", r.Output)
		}
	})
	t.Run("fail-integer-id", func(t *testing.T) {
		bin := stubBinary(t, badJSON, 0)
		sc := scenarioByID("M4-json-search-ulid")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL on integer id")
		}
	})
}

// TestPredicateUnknownFlagError verifies the M5 predicate.
func TestPredicateUnknownFlagError(t *testing.T) {
	t.Run("pass-mentions-scope", func(t *testing.T) {
		out := "error: unknown flag: --confidence\nValid flags: --scope, --provenance, --ttl\n"
		bin := stubBinary(t, out, 1)
		sc := scenarioByID("M5-unknown-flag-error")
		r := runScenario(bin, sc)
		if !r.Pass {
			t.Errorf("expected PASS; output=%q", r.Output)
		}
	})
	t.Run("fail-no-valid-flag-mentioned", func(t *testing.T) {
		out := "error: unknown flag: --confidence\n"
		bin := stubBinary(t, out, 1)
		sc := scenarioByID("M5-unknown-flag-error")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL when no valid flag mentioned")
		}
	})
	t.Run("fail-exits-zero", func(t *testing.T) {
		out := "error: unknown flag: --confidence\nuse --scope instead\n"
		bin := stubBinary(t, out, 0)
		sc := scenarioByID("M5-unknown-flag-error")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL when exit code is 0")
		}
	})
}

// TestPredicateScopeFromMarker verifies the M6 predicate.
func TestPredicateScopeFromMarker(t *testing.T) {
	goodOut := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nScope:      myproject\n"
	badOut := "ID:         01KXS5B55PBGZM993P6ZN4T3K8\nScope:      root\n"

	t.Run("pass", func(t *testing.T) {
		bin := stubBinary(t, goodOut, 0)
		sc := scenarioByID("M6-scope-from-marker-root")
		r := runScenario(bin, sc)
		if !r.Pass {
			t.Errorf("expected PASS; output=%q", r.Output)
		}
	})
	t.Run("fail-scope-root", func(t *testing.T) {
		bin := stubBinary(t, badOut, 0)
		sc := scenarioByID("M6-scope-from-marker-root")
		r := runScenario(bin, sc)
		if r.Pass {
			t.Errorf("expected FAIL when scope is 'root'")
		}
	})
}

// TestPredicateDedupSecondAdd verifies the M2-dedup predicate logic.
func TestPredicateDedupSecondAdd(t *testing.T) {
	ulid := "01KXS5B55PBGZM993P6ZN4T3K8"
	firstOut := fmt.Sprintf("ID:         %s\nContent:    test\n", ulid)
	secondOut := fmt.Sprintf("Updated existing entry %s (v2)\nID:         %s\n", ulid, ulid)
	noULIDSecond := "error: duplicate\n"

	t.Run("pass-update-mentions-ulid", func(t *testing.T) {
		ulid1 := reULID.FindString(firstOut)
		if ulid1 == "" {
			t.Fatal("test setup: no ULID in firstOut")
		}
		if !strings.Contains(secondOut, ulid1) {
			t.Errorf("predicate fail: secondOut=%q does not contain ulid %q", secondOut, ulid1)
		}
	})
	t.Run("fail-second-has-no-ulid", func(t *testing.T) {
		ulid1 := reULID.FindString(firstOut)
		if strings.Contains(noULIDSecond, ulid1) {
			t.Errorf("expected predicate FAIL but noULIDSecond=%q contains ulid", noULIDSecond)
		}
	})
}

// TestReULID verifies the ULID regex.
func TestReULID(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"01KXS5B55PBGZM993P6ZN4T3K8", true},
		{"01KTD44NBEHMHE0MPY6VGFCJR6", true},
		{"12345", false},
		{`"id":12345`, false},
		{`"id":"01KXS5B55PBGZM993P6ZN4T3K8"`, true},
	}
	for _, c := range cases {
		got := reULID.MatchString(c.s)
		if got != c.want {
			t.Errorf("reULID.MatchString(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// TestReULIDJSON verifies the JSON id regex.
func TestReULIDJSON(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{`"id":"01KXS5B55PBGZM993P6ZN4T3K8"`, true},
		{`"id": "01KXS5B55PBGZM993P6ZN4T3K8"`, true},
		{`"id":12345`, false},
		{`"id":"short"`, false},
	}
	for _, c := range cases {
		got := reULIDJSON.MatchString(c.s)
		if got != c.want {
			t.Errorf("reULIDJSON.MatchString(%q) = %v, want %v", c.s, got, c.want)
		}
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
