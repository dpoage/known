//go:build bench

package bench

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testdataPath(rel string) string {
	return filepath.Join(benchDir(), "testdata", rel)
}

// resolveAnswerer picks the right answerer based on environment variables.
//
// Priority:
//  1. BENCH_API_KEY + BENCH_BASE_URL → OpenAI-compatible (Minimax, Together, etc.)
//  2. ANTHROPIC_API_KEY → Anthropic Messages API
//  3. Neither → skip
func resolveAnswerer(t *testing.T) Answerer {
	t.Helper()

	model := os.Getenv(envBenchModel)
	baseURL := os.Getenv(envBenchBaseURL)
	thinking := os.Getenv(envBenchThinking) == "1"

	if apiKey := os.Getenv(envBenchAPIKey); apiKey != "" {
		if model == "" {
			t.Fatal(envBenchAPIKey + " set but " + envBenchModel + " is required")
		}
		return NewOpenAIAnswerer(apiKey, model, baseURL)
	}

	if apiKey := os.Getenv(envAnthropicKey); apiKey != "" {
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return NewAnthropicAnswerer(apiKey, model, baseURL, thinking)
	}

	t.Skip("No API key set — set " + envBenchAPIKey + " (OpenAI-compat) or " + envAnthropicKey + " to run")
	return nil
}

// TestEffectivenessRun runs the full effectiveness evaluation.
//
// OpenAI-compatible provider (Minimax, Together, Groq, etc.):
//
//	BENCH_API_KEY=... BENCH_MODEL=MiniMax-M2.7 BENCH_BASE_URL=https://api.minimaxi.chat/v1 \
//	  go test -tags bench ./bench/ -run TestEffectivenessRun -v -timeout 10m
//
// Anthropic (or Anthropic-compatible like MiniMax):
//
//	ANTHROPIC_API_KEY=... BENCH_MODEL=MiniMax-M2.7 BENCH_BASE_URL=https://api.minimax.io/anthropic \
//	  go test -tags bench ./bench/ -run TestEffectivenessRun -v -timeout 10m
func TestEffectivenessRun(t *testing.T) {
	ctx := context.Background()
	answerer := resolveAnswerer(t)

	t.Logf("Using answerer: %s", answerer.Name())

	// Parse optional question limit for smoke tests.
	maxQuestions := 0
	if s := os.Getenv(envBenchLimit); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxQuestions = n
			t.Logf("Limiting to %d questions per condition (%s=%s)", n, envBenchLimit, s)
		}
	}

	// Parse concurrency (default 1 = sequential).
	concurrency := 1
	if s := os.Getenv(envBenchConcurrency); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			concurrency = n
			t.Logf("Running %d concurrent API calls (%s=%s)", n, envBenchConcurrency, s)
		}
	}

	// Check if pipeliner memory DB exists to enable the with_memory condition.
	memoryDB := testdataPath("pipeliner_memory.db")
	conditions := []Condition{ConditionNoMemory, ConditionFullDump}
	recallCmd := ""
	if _, err := os.Stat(memoryDB); err == nil {
		conditions = []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump}
		recallCmd = fmt.Sprintf("KNOWN_DSN=%s known recall '{query}' --scope /pipeliner --limit 10 --threshold 0.3", memoryDB)
		t.Logf("Memory DB found at %s — enabling with_memory condition", memoryDB)
	} else {
		t.Logf("No memory DB at %s — skipping with_memory condition", memoryDB)
	}

	// Write progress to stderr so it streams live (t.Log buffers until test ends).
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: testdataPath("questions.yaml"),
		CodebasePath:  testdataPath("codebase"),
		RecallCommand: recallCmd,
		Conditions:    conditions,
		MaxQuestions:   maxQuestions,
		Concurrency:   concurrency,
		Log:         os.Stderr,
	}

	report, err := RunEffectiveness(ctx, cfg)
	if err != nil {
		t.Fatalf("RunEffectiveness: %v", err)
	}

	var reportBuf bytes.Buffer
	FormatEffectivenessReport(report, &reportBuf)
	t.Logf("\n%s", reportBuf.String())

	// Save baseline.
	baselinePath := testdataPath("effectiveness_baseline.json")
	baseline := Baseline{Scenarios: map[string]float64{}}
	baseline.AddEffectiveness(report)
	if err := SaveBaseline(baselinePath, baseline); err != nil {
		t.Errorf("save baseline: %v", err)
	}
	t.Logf("Baseline saved to %s", baselinePath)
}

// --- Stub Answerer: deterministic, hermetic, no-API-key end-to-end tests ---
//
// These tests drive RunEffectiveness through prompt assembly, all three
// conditions, answer scoring, and report formatting without any network
// access, API key, LLM, model cache, or the real `known` binary. The
// with_memory condition's RecallCommand is a plain `echo` — this exercises
// the same runRecall() shell-exec code path the real harness uses, just
// against a canned command instead of `known recall`.

// extractQuestionText recovers the EffectivenessQuestion.Question text that
// buildPrompt embedded in a generated prompt, by locating the trailing
// "Question: <text>\nAnswer:" block that every condition's prompt ends with
// (see buildPrompt in runner.go). Used only by the stub Answerer below to
// identify which question it is being asked, mirroring how a real LLM reads
// the prompt rather than requiring RunEffectiveness to pass IDs out-of-band.
func extractQuestionText(prompt string) string {
	const marker = "\nQuestion: "
	idx := strings.LastIndex(prompt, marker)
	if idx < 0 {
		return ""
	}
	rest := prompt[idx+len(marker):]
	return strings.TrimSuffix(rest, "\nAnswer:")
}

// canonicalAnswer renders a string that CheckAnswer accepts as correct for
// q, regardless of q.Answer.Type. Used to build a "perfect" stub Answerer.
func canonicalAnswer(q EffectivenessQuestion) string {
	switch q.Answer.Type {
	case "exact", "contains":
		return fmt.Sprintf("%v", q.Answer.Value)
	case "one_of":
		candidates := answerStrings(q.Answer.Value)
		if len(candidates) == 0 {
			return ""
		}
		return candidates[0]
	case "exact_set":
		return strings.Join(answerStrings(q.Answer.Value), ", ")
	default:
		return ""
	}
}

// stubAnswerer is a deterministic fake Answerer keyed by question ID. It
// never makes a network call: given a prompt, it recovers the question text
// via extractQuestionText, resolves that to a question ID via textToID (built
// from the real QuestionSet), and returns the canned answer for that ID —
// falling back to a fixed string for anything not in byID.
type stubAnswerer struct {
	name     string
	textToID map[string]string
	byID     map[string]string
	fallback string
}

// newStubAnswerer builds a stubAnswerer over qs. byID maps question ID ->
// canned answer; IDs absent from byID (or a nil byID) receive fallback.
func newStubAnswerer(name string, qs *QuestionSet, byID map[string]string, fallback string) *stubAnswerer {
	textToID := make(map[string]string)
	for _, sess := range qs.Sessions {
		for _, q := range sess.Questions {
			textToID[q.Question] = q.ID
		}
	}
	return &stubAnswerer{name: name, textToID: textToID, byID: byID, fallback: fallback}
}

func (s *stubAnswerer) Name() string { return s.name }

func (s *stubAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	id, ok := s.textToID[extractQuestionText(prompt)]
	if !ok {
		// A prompt we can't map back to a known question ID indicates a bug
		// in buildPrompt or extractQuestionText, not a real "wrong answer" —
		// surface it as an error so the caller's answer-error path fires
		// instead of silently scoring it as incorrect.
		return "", fmt.Errorf("stub answerer: prompt did not match any known question")
	}
	if ans, ok := s.byID[id]; ok {
		return ans, nil
	}
	return s.fallback, nil
}

func TestExtractQuestionText(t *testing.T) {
	dir := benchDir()
	qs, err := LoadQuestions(filepath.Join(dir, "testdata", "questions.yaml"))
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	q := qs.Sessions[0].Questions[0]

	codebaseDump, err := LoadCodebaseDump(filepath.Join(dir, "testdata", "codebase"))
	if err != nil {
		t.Fatalf("LoadCodebaseDump: %v", err)
	}
	fileListing := extractFileListing(codebaseDump)

	for _, cond := range []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump} {
		prompt, err := buildPrompt(q, cond, codebaseDump, fileListing, "echo '(stub recall output)'")
		if err != nil {
			t.Fatalf("buildPrompt(%s): %v", cond, err)
		}
		if got := extractQuestionText(prompt); got != q.Question {
			t.Errorf("extractQuestionText(%s) = %q, want %q", cond, got, q.Question)
		}
	}
}

// TestEffectivenessRun_StubAnswerer_Perfect drives the full three-condition
// harness end to end with a stub that always answers correctly, hermetically
// (no API key, no network, no `known` binary). It proves prompt assembly,
// all three conditions, scoring, and report generation all function on the
// current runner/effectiveness code, and gives a discrimination baseline: a
// perfect stub must score 1.0 everywhere.
func TestEffectivenessRun_StubAnswerer_Perfect(t *testing.T) {
	dir := benchDir()
	questionsPath := filepath.Join(dir, "testdata", "questions.yaml")
	qs, err := LoadQuestions(questionsPath)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}

	byID := make(map[string]string)
	for _, sess := range qs.Sessions {
		for _, q := range sess.Questions {
			byID[q.ID] = canonicalAnswer(q)
		}
	}

	answerer := newStubAnswerer("stub/perfect", qs, byID, "")
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: questionsPath,
		CodebasePath:  filepath.Join(dir, "testdata", "codebase"),
		RecallCommand: "echo '(stub recall output — hermetic, no known binary or model needed)'",
		Concurrency:   4,
	}

	report, err := RunEffectiveness(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunEffectiveness: %v", err)
	}

	for _, cond := range []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump} {
		res := report.Results[cond]
		if res == nil {
			t.Fatalf("no result for condition %s", cond)
		}
		if res.OverallScore != 1.0 {
			t.Errorf("condition %s: overall score = %v, want 1.0 for a perfect stub", cond, res.OverallScore)
		}
		for sess, score := range res.SessionScores {
			if score != 1.0 {
				t.Errorf("condition %s session %d: score = %v, want 1.0", cond, sess, score)
			}
		}
	}

	var buf bytes.Buffer
	FormatEffectivenessReport(report, &buf)
	output := buf.String()
	for _, want := range []string{"AGENT EFFECTIVENESS", "No Memory", "With Memory", "Full Dump", "Overall"} {
		if !strings.Contains(output, want) {
			t.Errorf("report output missing %q:\n%s", want, output)
		}
	}
	t.Logf("stub-answerer end-to-end report (perfect stub):\n%s", output)
}

// TestEffectivenessRun_StubAnswerer_AllWrong proves discrimination: a stub
// that never gives a genuinely correct answer must score exactly 0, not just
// "less than the perfect stub". It also self-checks the chosen wrong answer
// against every question's real CheckAnswer logic so the assertion can't
// pass by accident (e.g. a "contains" question whose expected substring
// happens to appear in the wrong answer).
func TestEffectivenessRun_StubAnswerer_AllWrong(t *testing.T) {
	dir := benchDir()
	questionsPath := filepath.Join(dir, "testdata", "questions.yaml")
	qs, err := LoadQuestions(questionsPath)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}

	const wrongAnswer = "zzz-definitely-not-the-real-answer-zzz"
	for _, sess := range qs.Sessions {
		for _, q := range sess.Questions {
			if CheckAnswer(q, wrongAnswer) {
				t.Fatalf("fixture bug: wrong answer %q accidentally satisfies question %s (%s)", wrongAnswer, q.ID, q.Answer.Type)
			}
		}
	}

	answerer := newStubAnswerer("stub/all-wrong", qs, nil, wrongAnswer)
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: questionsPath,
		CodebasePath:  filepath.Join(dir, "testdata", "codebase"),
		RecallCommand: "echo '(stub recall output)'",
		Conditions:    []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump},
	}

	report, err := RunEffectiveness(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunEffectiveness: %v", err)
	}

	for _, cond := range []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump} {
		res := report.Results[cond]
		if res == nil {
			t.Fatalf("no result for condition %s", cond)
		}
		if res.OverallScore != 0 {
			t.Errorf("condition %s: overall score = %v, want 0 for an always-wrong stub", cond, res.OverallScore)
		}
	}
}

// TestEffectivenessRun_StubAnswerer_PartialCredit checks graded discrimination
// (not just the 0/1 extremes): a stub that answers exactly one question per
// session correctly must score exactly 1/10 on every session.
func TestEffectivenessRun_StubAnswerer_PartialCredit(t *testing.T) {
	dir := benchDir()
	questionsPath := filepath.Join(dir, "testdata", "questions.yaml")
	qs, err := LoadQuestions(questionsPath)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}

	const wrongAnswer = "zzz-definitely-not-the-real-answer-zzz"
	byID := make(map[string]string)
	for _, sess := range qs.Sessions {
		for i, q := range sess.Questions {
			if i == 0 {
				byID[q.ID] = canonicalAnswer(q) // first question per session: correct
			} else {
				byID[q.ID] = wrongAnswer
			}
		}
	}

	answerer := newStubAnswerer("stub/partial", qs, byID, wrongAnswer)
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: questionsPath,
		CodebasePath:  filepath.Join(dir, "testdata", "codebase"),
		RecallCommand: "echo '(stub recall output)'",
		Conditions:    []Condition{ConditionNoMemory},
	}

	report, err := RunEffectiveness(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunEffectiveness: %v", err)
	}

	res := report.Results[ConditionNoMemory]
	if res == nil {
		t.Fatal("no result for no_memory condition")
	}
	const want = 0.1 // 1 of 10 questions per session
	for sess, score := range res.SessionScores {
		if score != want {
			t.Errorf("session %d: score = %v, want %v (1/10 correct)", sess, score, want)
		}
	}
	if res.OverallScore != want {
		t.Errorf("overall score = %v, want %v", res.OverallScore, want)
	}
}
