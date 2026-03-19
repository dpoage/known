//go:build bench

package bench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataPath(rel string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", rel)
}

// TestEffectivenessRun runs the full effectiveness evaluation using the
// Anthropic API. Requires ANTHROPIC_API_KEY to be set.
//
// Usage:
//
//	ANTHROPIC_API_KEY=sk-... go test -tags bench ./bench/ -run TestEffectivenessRun -v
//
// Override the model with BENCH_MODEL:
//
//	BENCH_MODEL=claude-sonnet-4-5-20241022 go test -tags bench ./bench/ -run TestEffectivenessRun -v
func TestEffectivenessRun(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping effectiveness run")
	}

	model := os.Getenv("BENCH_MODEL")
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}

	ctx := context.Background()
	answerer := NewAnthropicAnswerer(model)

	var logBuf bytes.Buffer
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: testdataPath("questions.yaml"),
		CodebasePath:  testdataPath("codebase"),
		// RecallCommand would be: "known recall '{query}' --scope pipeliner"
		// but the seed memory DB doesn't exist for this codebase yet,
		// so we skip the with_memory condition for now.
		Conditions: []Condition{ConditionNoMemory, ConditionFullDump},
		Log:        &logBuf,
	}

	report, err := RunEffectiveness(ctx, cfg)
	if err != nil {
		t.Fatalf("RunEffectiveness: %v", err)
	}

	t.Logf("\n%s", logBuf.String())

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
}
