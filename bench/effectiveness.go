//go:build bench

package bench

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EffectivenessQuestion represents one question from questions.yaml.
type EffectivenessQuestion struct {
	ID          string `yaml:"id"`
	Question    string `yaml:"question"`
	RecallQuery string `yaml:"recall_query"`
	Answer      Answer `yaml:"answer"`
}

// Answer holds the expected answer for a question.
type Answer struct {
	Type  string `yaml:"type"`  // exact, one_of, exact_set, contains
	Value any    `yaml:"value"` // string or []string depending on type
}

// Session groups questions belonging to one session.
type Session struct {
	Session     int                     `yaml:"session"`
	Name        string                  `yaml:"name"`
	Description string                  `yaml:"description"`
	DependsOn   []int                   `yaml:"depends_on_knowledge_from"`
	Questions   []EffectivenessQuestion `yaml:"questions"`
}

// QuestionSet is the top-level structure for questions.yaml.
type QuestionSet struct {
	Scenario string    `yaml:"scenario"`
	Sessions []Session `yaml:"sessions"`
}

// Condition represents a test condition.
type Condition string

const (
	ConditionNoMemory   Condition = "no_memory"
	ConditionWithMemory Condition = "with_memory"
	ConditionFullDump   Condition = "full_dump"
)

// EffectivenessResult captures results for one condition.
type EffectivenessResult struct {
	Condition     Condition
	SessionScores map[int]float64 // session number -> accuracy
	OverallScore  float64
	Answers       map[string]string // question ID -> given answer
}

// EffectivenessReport is the full comparison.
type EffectivenessReport struct {
	Results      map[Condition]*EffectivenessResult
	SessionDelta map[int]float64 // session -> (with_memory - no_memory)
	OverallDelta float64
}

// LoadQuestions parses questions.yaml from the given path.
func LoadQuestions(path string) (*QuestionSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read questions file: %w", err)
	}
	var qs QuestionSet
	if err := yaml.Unmarshal(data, &qs); err != nil {
		return nil, fmt.Errorf("parse questions YAML: %w", err)
	}
	return &qs, nil
}

// answerStrings extracts the string list from an Answer.Value.
// For scalar values it returns a single-element slice.
func answerStrings(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case []string:
		return val
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

// CheckAnswer verifies a given answer against the expected answer.
func CheckAnswer(question EffectivenessQuestion, givenAnswer string) bool {
	given := strings.TrimSpace(givenAnswer)
	ans := question.Answer

	switch ans.Type {
	case "exact":
		expected := strings.TrimSpace(fmt.Sprintf("%v", ans.Value))
		return strings.EqualFold(given, expected)

	case "one_of":
		for _, candidate := range answerStrings(ans.Value) {
			if strings.EqualFold(given, strings.TrimSpace(candidate)) {
				return true
			}
		}
		return false

	case "exact_set":
		// Split given answer on commas or newlines, trim and lowercase each element.
		givenParts := strings.FieldsFunc(given, func(r rune) bool {
			return r == ',' || r == '\n'
		})
		givenSet := make([]string, 0, len(givenParts))
		for _, p := range givenParts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				givenSet = append(givenSet, strings.ToLower(trimmed))
			}
		}
		sort.Strings(givenSet)

		expectedParts := answerStrings(ans.Value)
		expectedSet := make([]string, 0, len(expectedParts))
		for _, p := range expectedParts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				expectedSet = append(expectedSet, strings.ToLower(trimmed))
			}
		}
		sort.Strings(expectedSet)

		if len(givenSet) != len(expectedSet) {
			return false
		}
		for i := range givenSet {
			if givenSet[i] != expectedSet[i] {
				return false
			}
		}
		return true

	case "contains":
		expected := strings.TrimSpace(fmt.Sprintf("%v", ans.Value))
		return strings.Contains(strings.ToLower(given), strings.ToLower(expected))

	default:
		return false
	}
}

// ScoreEffectiveness computes per-session and overall accuracy for a set of answers.
func ScoreEffectiveness(questions *QuestionSet, answers map[string]string) *EffectivenessResult {
	result := &EffectivenessResult{
		SessionScores: make(map[int]float64),
		Answers:       answers,
	}

	totalCorrect := 0
	totalQuestions := 0

	for _, sess := range questions.Sessions {
		if len(sess.Questions) == 0 {
			continue
		}
		correct := 0
		for _, q := range sess.Questions {
			totalQuestions++
			given, ok := answers[q.ID]
			if ok && CheckAnswer(q, given) {
				correct++
				totalCorrect++
			}
		}
		result.SessionScores[sess.Session] = float64(correct) / float64(len(sess.Questions))
	}

	if totalQuestions > 0 {
		result.OverallScore = float64(totalCorrect) / float64(totalQuestions)
	}

	return result
}

// CompareEffectiveness computes deltas between conditions.
func CompareEffectiveness(results map[Condition]*EffectivenessResult) *EffectivenessReport {
	report := &EffectivenessReport{
		Results:      results,
		SessionDelta: make(map[int]float64),
	}

	noMem := results[ConditionNoMemory]
	withMem := results[ConditionWithMemory]

	if noMem != nil && withMem != nil {
		for sess, withScore := range withMem.SessionScores {
			noScore := noMem.SessionScores[sess]
			report.SessionDelta[sess] = withScore - noScore
		}
		report.OverallDelta = withMem.OverallScore - noMem.OverallScore
	}

	return report
}

// FormatEffectivenessReport writes a comparison table to w.
func FormatEffectivenessReport(report *EffectivenessReport, w io.Writer) {
	fmt.Fprintln(w, "=== AGENT EFFECTIVENESS ===")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-9s| %-10s| %-12s| %-10s| %s\n",
		"Session ", "No Memory ", "With Memory ", "Full Dump ", "Delta (Memory vs None)")
	fmt.Fprintln(w, strings.Repeat("-", 9)+"|"+strings.Repeat("-", 11)+"|"+
		strings.Repeat("-", 13)+"|"+strings.Repeat("-", 11)+"|"+strings.Repeat("-", 22))

	// Collect session numbers and sort them.
	sessions := make([]int, 0)
	for _, r := range report.Results {
		for s := range r.SessionScores {
			sessions = append(sessions, s)
		}
		break
	}
	// If no results had sessions, try all results.
	if len(sessions) == 0 {
		seen := map[int]bool{}
		for _, r := range report.Results {
			for s := range r.SessionScores {
				if !seen[s] {
					sessions = append(sessions, s)
					seen[s] = true
				}
			}
		}
	}
	sort.Ints(sessions)

	getScore := func(cond Condition, sess int) string {
		r, ok := report.Results[cond]
		if !ok {
			return "  -   "
		}
		score, ok := r.SessionScores[sess]
		if !ok {
			return "  -   "
		}
		return fmt.Sprintf("%.2f", score)
	}

	for _, s := range sessions {
		delta := ""
		if d, ok := report.SessionDelta[s]; ok {
			delta = fmt.Sprintf("%+.2f", d)
		}
		fmt.Fprintf(w, "%-9s| %-10s| %-12s| %-10s| %s\n",
			fmt.Sprintf("%d", s),
			getScore(ConditionNoMemory, s),
			getScore(ConditionWithMemory, s),
			getScore(ConditionFullDump, s),
			delta)
	}

	fmt.Fprintln(w, strings.Repeat("-", 9)+"|"+strings.Repeat("-", 11)+"|"+
		strings.Repeat("-", 13)+"|"+strings.Repeat("-", 11)+"|"+strings.Repeat("-", 22))

	getOverall := func(cond Condition) string {
		r, ok := report.Results[cond]
		if !ok {
			return "  -   "
		}
		return fmt.Sprintf("%.2f", r.OverallScore)
	}

	fmt.Fprintf(w, "%-9s| %-10s| %-12s| %-10s| %+.2f\n",
		"Overall",
		getOverall(ConditionNoMemory),
		getOverall(ConditionWithMemory),
		getOverall(ConditionFullDump),
		report.OverallDelta)
}

// LoadCodebaseDump concatenates all .go and .yaml files from the synthetic
// codebase directory into a single string for the full_dump condition.
func LoadCodebaseDump(dir string) (string, error) {
	var buf strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".yaml" {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		fmt.Fprintf(&buf, "=== %s ===\n", rel)
		buf.Write(data)
		buf.WriteByte('\n')
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk codebase dir: %w", err)
	}
	return buf.String(), nil
}
