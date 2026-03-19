//go:build bench

package bench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
		return NewAnthropicAnswerer(apiKey, model, baseURL)
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

	var logBuf bytes.Buffer
	cfg := RunnerConfig{
		Answerer:      answerer,
		QuestionsPath: testdataPath("questions.yaml"),
		CodebasePath:  testdataPath("codebase"),
		// RecallCommand would be: "known recall '{query}' --scope pipeliner"
		// Skip with_memory for now — pipeliner knowledge not seeded yet.
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
	t.Logf("Baseline saved to %s", baselinePath)
}
