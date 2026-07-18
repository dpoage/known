//go:build bench

package bench

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file location")
	}
	return filepath.Join(filepath.Dir(filename), "testdata")
}

func TestLoadQuestions(t *testing.T) {
	dir := testdataDir(t)
	qs, err := LoadQuestions(filepath.Join(dir, "questions.yaml"))
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}

	if qs.Scenario != "pipeliner-codebase" {
		t.Errorf("scenario = %q, want %q", qs.Scenario, "pipeliner-codebase")
	}

	if len(qs.Sessions) != 5 {
		t.Fatalf("sessions = %d, want 5", len(qs.Sessions))
	}

	totalQuestions := 0
	for _, s := range qs.Sessions {
		totalQuestions += len(s.Questions)
	}
	if totalQuestions != 50 {
		t.Errorf("total questions = %d, want 50", totalQuestions)
	}

	// Verify each session has 10 questions.
	for _, s := range qs.Sessions {
		if len(s.Questions) != 10 {
			t.Errorf("session %d has %d questions, want 10", s.Session, len(s.Questions))
		}
	}
}

func TestCheckAnswer_Exact(t *testing.T) {
	q := EffectivenessQuestion{
		Answer: Answer{Type: "exact", Value: "main.go"},
	}

	tests := []struct {
		given string
		want  bool
	}{
		{"main.go", true},
		{"Main.go", true},       // case insensitive
		{"  main.go  ", true},   // trim whitespace
		{"MAIN.GO", true},
		{"main.py", false},
		{"", false},
	}
	for _, tt := range tests {
		got := CheckAnswer(q, tt.given)
		if got != tt.want {
			t.Errorf("CheckAnswer(exact, %q) = %v, want %v", tt.given, got, tt.want)
		}
	}
}

func TestCheckAnswer_OneOf(t *testing.T) {
	q := EffectivenessQuestion{
		Answer: Answer{
			Type:  "one_of",
			Value: []any{"none", "nothing"},
		},
	}

	tests := []struct {
		given string
		want  bool
	}{
		{"none", true},
		{"Nothing", true},  // case insensitive
		{" none ", true},   // trim
		{"some", false},
		{"", false},
	}
	for _, tt := range tests {
		got := CheckAnswer(q, tt.given)
		if got != tt.want {
			t.Errorf("CheckAnswer(one_of, %q) = %v, want %v", tt.given, got, tt.want)
		}
	}
}

func TestCheckAnswer_ExactSet(t *testing.T) {
	q := EffectivenessQuestion{
		Answer: Answer{
			Type:  "exact_set",
			Value: []any{"Name", "Process", "Validate"},
		},
	}

	tests := []struct {
		given string
		want  bool
	}{
		{"Name, Process, Validate", true},
		{"Validate, Name, Process", true},     // order independent
		{"name, process, validate", true},     // case insensitive
		{" Name , Process , Validate ", true},          // whitespace
		{"Name\nProcess\nValidate", true},              // newline separated
		{"Name\n Process \n Validate ", true},           // newline + whitespace
		{"Name, Process", false},                       // missing element
		{"Name, Process, Validate, Extra", false},
	}
	for _, tt := range tests {
		got := CheckAnswer(q, tt.given)
		if got != tt.want {
			t.Errorf("CheckAnswer(exact_set, %q) = %v, want %v", tt.given, got, tt.want)
		}
	}
}

func TestCheckAnswer_Contains(t *testing.T) {
	q := EffectivenessQuestion{
		Answer: Answer{Type: "contains", Value: "fail"},
	}

	tests := []struct {
		given string
		want  bool
	}{
		{"it will fail fast on error", true},
		{"FAIL immediately", true},
		{"success", false},
		{"", false},
	}
	for _, tt := range tests {
		got := CheckAnswer(q, tt.given)
		if got != tt.want {
			t.Errorf("CheckAnswer(contains, %q) = %v, want %v", tt.given, got, tt.want)
		}
	}
}

// TestCheckAnswer_Adversarial exercises near-miss wrong answers,
// case/whitespace variants, newline-separated sets, and empty answers across
// every answer type — including the guards CheckAnswer adds against blank
// given answers and blank/misconfigured expected values (see CheckAnswer's
// doc comment for why those matter: without them a truncated LLM response,
// or a malformed question fixture, could score as "correct").
func TestCheckAnswer_Adversarial(t *testing.T) {
	t.Run("exact near-miss and whitespace", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "exact", Value: "config.Load"}}
		tests := []struct {
			given string
			want  bool
		}{
			{"config.Load", true},
			{"CONFIG.LOAD", true},
			{"\tconfig.Load\n", true}, // tab/newline whitespace, not just spaces
			{"config.load2", false},   // near miss: extra char
			{"onfig.Load", false},     // near miss: missing char
			{"config .Load", false},   // near miss: internal whitespace inserted
			{"config.Load ", true},    // trailing space trimmed
			{"", false},               // empty given
		}
		for _, tt := range tests {
			if got := CheckAnswer(q, tt.given); got != tt.want {
				t.Errorf("CheckAnswer(exact, %q) = %v, want %v", tt.given, got, tt.want)
			}
		}
	})

	t.Run("exact blank expected never matches", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "exact", Value: ""}}
		if CheckAnswer(q, "anything") {
			t.Error("exact with blank expected value should never match a non-blank answer")
		}
		if CheckAnswer(q, "") {
			t.Error("exact with blank expected value should never match a blank given answer")
		}
	})

	t.Run("one_of near-miss and empty", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "one_of", Value: []any{"csv-transform", "csv"}}}
		tests := []struct {
			given string
			want  bool
		}{
			{"csv-transform", true},
			{"CSV-TRANSFORM", true},
			{" csv ", true},
			{"csv-transforms", false}, // near miss: trailing char
			{"json-transform", false}, // wrong candidate entirely
			{"", false},
		}
		for _, tt := range tests {
			if got := CheckAnswer(q, tt.given); got != tt.want {
				t.Errorf("CheckAnswer(one_of, %q) = %v, want %v", tt.given, got, tt.want)
			}
		}
	})

	t.Run("exact_set near-miss, newline-separated, and empty", func(t *testing.T) {
		q := EffectivenessQuestion{
			Answer: Answer{Type: "exact_set", Value: []any{"auth", "config", "errors"}},
		}
		tests := []struct {
			given string
			want  bool
		}{
			{"auth, config, errors", true},
			{"auth\nconfig\nerrors", true},   // pure newline separation
			{"auth\r\nconfig\r\nerrors", true}, // CRLF line endings
			{"errors, auth, config", true},   // order independent
			{"AUTH, Config, ERRORS", true},   // case insensitive
			{"auth, config", false},          // missing element (near miss: subset)
			{"auth, config, errors, output", false}, // extra element (near miss: superset)
			{"auth, config, error", false},   // near miss: singular vs plural
			{"", false},                      // empty given
			{",,,", false},                   // punctuation-only given, all tokens empty
		}
		for _, tt := range tests {
			if got := CheckAnswer(q, tt.given); got != tt.want {
				t.Errorf("CheckAnswer(exact_set, %q) = %v, want %v", tt.given, got, tt.want)
			}
		}
	})

	t.Run("exact_set blank expected never matches", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "exact_set", Value: []any{}}}
		if CheckAnswer(q, "anything, at, all") {
			t.Error("exact_set with an empty expected set should never match a non-empty given set")
		}
		// Critical case: given must ALSO tokenize to an empty set (not just a
		// differently-sized one) so the assertion actually exercises the
		// len(expectedSet)==0 guard rather than passing "by accident" via the
		// unrelated length-mismatch check (0 given tokens vs 3 above).
		// Without the guard, an empty given set trivially equals an empty
		// expected set (both length 0), incorrectly scoring "correct".
		if CheckAnswer(q, ",,, , ,") {
			t.Error("exact_set with an empty expected set should never match an empty given set " +
				"(given tokenizes to zero elements too — must not pass via accidental length equality)")
		}
	})

	t.Run("contains near-miss and empty", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "contains", Value: "blank import"}}
		tests := []struct {
			given string
			want  bool
		}{
			{"uses a blank import to trigger init", true},
			{"BLANK IMPORT triggers init", true},
			{"blank imports", true}, // superstring still contains the substring
			{"blank imp0rt", false}, // near miss: character substitution
			{"import", false},       // partial word only, not the full substring
			{"", false},
		}
		for _, tt := range tests {
			if got := CheckAnswer(q, tt.given); got != tt.want {
				t.Errorf("CheckAnswer(contains, %q) = %v, want %v", tt.given, got, tt.want)
			}
		}
	})

	t.Run("contains blank expected never matches", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "contains", Value: ""}}
		if CheckAnswer(q, "literally any non-empty answer") {
			t.Error("contains with a blank expected substring should never match — " +
				"strings.Contains(x, \"\") is trivially true and must not leak through")
		}
	})

	t.Run("unknown type never matches", func(t *testing.T) {
		q := EffectivenessQuestion{Answer: Answer{Type: "bogus-type", Value: "x"}}
		if CheckAnswer(q, "x") {
			t.Error("unrecognized answer type should never match")
		}
	})
}

func TestScoreEffectiveness(t *testing.T) {
	qs := &QuestionSet{
		Sessions: []Session{
			{
				Session: 1,
				Questions: []EffectivenessQuestion{
					{ID: "q1", Answer: Answer{Type: "exact", Value: "yes"}},
					{ID: "q2", Answer: Answer{Type: "exact", Value: "no"}},
				},
			},
			{
				Session: 2,
				Questions: []EffectivenessQuestion{
					{ID: "q3", Answer: Answer{Type: "exact", Value: "foo"}},
					{ID: "q4", Answer: Answer{Type: "exact", Value: "bar"}},
				},
			},
		},
	}

	answers := map[string]string{
		"q1": "yes",
		"q2": "wrong",
		"q3": "foo",
		"q4": "bar",
	}

	result := ScoreEffectiveness(qs, answers)

	if result.SessionScores[1] != 0.5 {
		t.Errorf("session 1 score = %v, want 0.5", result.SessionScores[1])
	}
	if result.SessionScores[2] != 1.0 {
		t.Errorf("session 2 score = %v, want 1.0", result.SessionScores[2])
	}
	if result.OverallScore != 0.75 {
		t.Errorf("overall = %v, want 0.75", result.OverallScore)
	}
}

func almostEqual(a, b, epsilon float64) bool {
	diff := a - b
	return diff > -epsilon && diff < epsilon
}

func TestCompareEffectiveness(t *testing.T) {
	results := map[Condition]*EffectivenessResult{
		ConditionNoMemory: {
			Condition:     ConditionNoMemory,
			SessionScores: map[int]float64{1: 0.5, 2: 0.3},
			OverallScore:  0.4,
		},
		ConditionWithMemory: {
			Condition:     ConditionWithMemory,
			SessionScores: map[int]float64{1: 0.8, 2: 0.7},
			OverallScore:  0.75,
		},
	}

	report := CompareEffectiveness(results)

	const eps = 0.001
	if !almostEqual(report.SessionDelta[1], 0.3, eps) {
		t.Errorf("session 1 delta = %v, want 0.3", report.SessionDelta[1])
	}
	if !almostEqual(report.SessionDelta[2], 0.4, eps) {
		t.Errorf("session 2 delta = %v, want 0.4", report.SessionDelta[2])
	}
	if !almostEqual(report.OverallDelta, 0.35, eps) {
		t.Errorf("overall delta = %v, want 0.35", report.OverallDelta)
	}
}

func TestLoadCodebaseDump(t *testing.T) {
	dir := testdataDir(t)
	codebaseDir := filepath.Join(dir, "codebase")

	dump, err := LoadCodebaseDump(codebaseDir)
	if err != nil {
		t.Fatalf("LoadCodebaseDump: %v", err)
	}

	if len(dump) == 0 {
		t.Fatal("codebase dump is empty")
	}

	// Should contain main.go content.
	if !strings.Contains(dump, "func main()") {
		t.Error("dump does not contain func main()")
	}

	// Should contain yaml config content.
	if !strings.Contains(dump, "csv-transform") {
		t.Error("dump does not contain csv-transform")
	}

	// Should contain file headers.
	if !strings.Contains(dump, "=== main.go ===") {
		t.Error("dump does not contain main.go header")
	}
}

func TestFormatEffectivenessReport(t *testing.T) {
	report := &EffectivenessReport{
		Results: map[Condition]*EffectivenessResult{
			ConditionNoMemory: {
				Condition:     ConditionNoMemory,
				SessionScores: map[int]float64{1: 0.70, 2: 0.55},
				OverallScore:  0.43,
			},
			ConditionWithMemory: {
				Condition:     ConditionWithMemory,
				SessionScores: map[int]float64{1: 0.85, 2: 0.80},
				OverallScore:  0.76,
			},
			ConditionFullDump: {
				Condition:     ConditionFullDump,
				SessionScores: map[int]float64{1: 0.90, 2: 0.75},
				OverallScore:  0.66,
			},
		},
		SessionDelta: map[int]float64{1: 0.15, 2: 0.25},
		OverallDelta: 0.33,
	}

	var buf bytes.Buffer
	FormatEffectivenessReport(report, &buf)
	output := buf.String()

	// Check headers.
	if !strings.Contains(output, "AGENT EFFECTIVENESS") {
		t.Error("missing AGENT EFFECTIVENESS header")
	}
	if !strings.Contains(output, "No Memory") {
		t.Error("missing No Memory column header")
	}
	if !strings.Contains(output, "With Memory") {
		t.Error("missing With Memory column header")
	}
	if !strings.Contains(output, "Full Dump") {
		t.Error("missing Full Dump column header")
	}

	// Check values appear.
	if !strings.Contains(output, "0.70") {
		t.Error("missing no_memory session 1 score 0.70")
	}
	if !strings.Contains(output, "0.85") {
		t.Error("missing with_memory session 1 score 0.85")
	}
	if !strings.Contains(output, "Overall") {
		t.Error("missing Overall row")
	}
}
