//go:build bench

package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaselineFromReport(t *testing.T) {
	report := BenchmarkReport{
		Scenarios: []ScenarioScore{
			{Name: "A", Average: 0.90},
			{Name: "B", Average: 0.80},
		},
		Overall: 0.85,
		Ablation: map[string]AblationResult{
			"Graph Expansion": {FullScore: 0.85, WithoutScore: 0.75, Lift: 0.10},
		},
	}
	b := BaselineFromReport(report)
	assert.Equal(t, 0.85, b.Overall)
	assert.Equal(t, 0.90, b.Scenarios["A"])
	assert.Equal(t, 0.80, b.Scenarios["B"])
	assert.Equal(t, 0.10, b.Ablation["Graph Expansion"])
}

func TestBaselineAddEffectiveness(t *testing.T) {
	b := Baseline{Scenarios: map[string]float64{}}
	report := &EffectivenessReport{
		Results: map[Condition]*EffectivenessResult{
			ConditionWithMemory: {
				Condition:     ConditionWithMemory,
				SessionScores: map[int]float64{1: 0.80, 2: 0.70},
				OverallScore:  0.75,
			},
		},
	}
	b.AddEffectiveness(report)
	assert.Equal(t, 0.75, b.EffectivenessOverall["with_memory"])
	assert.Equal(t, 0.80, b.Effectiveness["with_memory"][1])
}

func TestSaveAndLoadBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	original := Baseline{
		Scenarios: map[string]float64{"A": 0.95},
		Overall:   0.95,
		Ablation:  map[string]float64{"FTS5": 0.03},
	}
	require.NoError(t, SaveBaseline(path, original))

	loaded, err := LoadBaseline(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, original.Overall, loaded.Overall)
	assert.Equal(t, original.Scenarios["A"], loaded.Scenarios["A"])
	assert.Equal(t, original.Ablation["FTS5"], loaded.Ablation["FTS5"])
}

func TestLoadBaseline_NotExist(t *testing.T) {
	b, err := LoadBaseline("/nonexistent/path/baseline.json")
	require.NoError(t, err)
	assert.Nil(t, b)
}

func TestCompareBaseline_NoRegression(t *testing.T) {
	old := Baseline{
		Scenarios: map[string]float64{"A": 0.90},
		Overall:   0.90,
	}
	current := Baseline{
		Scenarios: map[string]float64{"A": 0.92},
		Overall:   0.92,
	}
	regs := CompareBaseline(old, current)
	assert.Empty(t, regs)
}

func TestCompareBaseline_ScenarioRegression(t *testing.T) {
	old := Baseline{
		Scenarios: map[string]float64{"A": 0.90, "B": 0.80},
		Overall:   0.85,
	}
	current := Baseline{
		Scenarios: map[string]float64{"A": 0.85, "B": 0.80}, // A dropped 0.05
		Overall:   0.825,
	}
	regs := CompareBaseline(old, current)
	require.Len(t, regs, 2) // scenario A + overall
	assert.Equal(t, "overall", regs[0].Area)
	assert.Equal(t, "scenario:A", regs[1].Area)
	assert.InDelta(t, -0.05, regs[1].Delta, 0.001)
}

func TestCompareBaseline_AblationRegression(t *testing.T) {
	old := Baseline{
		Scenarios: map[string]float64{},
		Ablation:  map[string]float64{"Graph": 0.10},
	}
	current := Baseline{
		Scenarios: map[string]float64{},
		Ablation:  map[string]float64{"Graph": 0.05}, // lift dropped
	}
	regs := CompareBaseline(old, current)
	require.Len(t, regs, 1)
	assert.Equal(t, "ablation:Graph", regs[0].Area)
}

func TestCompareBaseline_EffectivenessRegression(t *testing.T) {
	old := Baseline{
		Scenarios:            map[string]float64{},
		EffectivenessOverall: map[string]float64{"with_memory": 0.80},
	}
	current := Baseline{
		Scenarios:            map[string]float64{},
		EffectivenessOverall: map[string]float64{"with_memory": 0.70}, // dropped 0.10
	}
	regs := CompareBaseline(old, current)
	require.Len(t, regs, 1)
	assert.Equal(t, "effectiveness:with_memory", regs[0].Area)
}

func TestCompareBaseline_WithinThreshold(t *testing.T) {
	old := Baseline{
		Scenarios: map[string]float64{"A": 0.90},
		Overall:   0.90,
	}
	current := Baseline{
		Scenarios: map[string]float64{"A": 0.885}, // only 0.015 drop, within 0.02 threshold
		Overall:   0.885,
	}
	regs := CompareBaseline(old, current)
	assert.Empty(t, regs)
}

func TestFormatRegressions_None(t *testing.T) {
	var buf bytes.Buffer
	n := FormatRegressions(nil, &buf)
	assert.Equal(t, 0, n)
	assert.Contains(t, buf.String(), "PASS")
}

func TestFormatRegressions_WithRegressions(t *testing.T) {
	regs := []Regression{
		{Area: "scenario:A", Baseline: 0.90, Current: 0.85, Delta: -0.05},
	}
	var buf bytes.Buffer
	n := FormatRegressions(regs, &buf)
	assert.Equal(t, 1, n)
	assert.Contains(t, buf.String(), "FAIL")
	assert.Contains(t, buf.String(), "scenario:A")
}

func TestSaveBaseline_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.json")

	b := Baseline{
		Scenarios:            map[string]float64{"X": 0.123},
		Overall:              0.456,
		Ablation:             map[string]float64{"F": 0.078},
		EffectivenessOverall: map[string]float64{"with_memory": 0.789},
		Effectiveness:        map[string]map[int]float64{"with_memory": {1: 0.8, 2: 0.7}},
	}
	require.NoError(t, SaveBaseline(path, b))

	// Verify it's valid JSON.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"overall"`)

	loaded, err := LoadBaseline(path)
	require.NoError(t, err)
	assert.Equal(t, b.Overall, loaded.Overall)
	assert.Equal(t, b.Effectiveness["with_memory"][1], loaded.Effectiveness["with_memory"][1])
}
