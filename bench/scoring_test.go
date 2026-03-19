//go:build bench

package bench

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreQuery_Perfect(t *testing.T) {
	expect := QueryExpectation{
		QueryText:        "test query",
		MustInclude:      []string{"a", "b"},
		MustExclude:      []string{"c"},
		MustRankAbove:    map[string]string{"a": "b"},
		MustFlagConflict: []string{"a"},
		ExpectReach:      map[string]string{"a": "direct", "b": "expansion"},
	}
	actual := QueryResult{
		ReturnedIDs: []string{"a", "b"},
		HasConflict: map[string]bool{"a": true},
		ReachMethod: map[string]string{"a": "direct", "b": "expansion"},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 1.0, score.Total)
	assert.Equal(t, 1.0, score.Inclusion)
	assert.Equal(t, 1.0, score.Exclusion)
	assert.Equal(t, 1.0, score.Ranking)
	assert.Equal(t, 1.0, score.Conflict)
	assert.Equal(t, 1.0, score.Reach)
	assert.Empty(t, score.Failures)
}

func TestScoreQuery_AllMissing(t *testing.T) {
	expect := QueryExpectation{
		QueryText:        "bad query",
		MustInclude:      []string{"a", "b"},
		MustExclude:      []string{"c"},
		MustRankAbove:    map[string]string{"a": "b"},
		MustFlagConflict: []string{"a"},
		ExpectReach:      map[string]string{"a": "direct"},
	}
	actual := QueryResult{
		ReturnedIDs: []string{"c"},
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 0.0, score.Inclusion)
	assert.Equal(t, 0.0, score.Exclusion)
	assert.Equal(t, 0.0, score.Ranking)
	assert.Equal(t, 0.0, score.Conflict)
	assert.Equal(t, 0.0, score.Reach)
	assert.Equal(t, 0.0, score.Total)
	assert.Len(t, score.Failures, 6) // 2 missing + 1 excluded + 1 ranking + 1 conflict + 1 reach mismatch
}

func TestScoreQuery_PartialInclusion(t *testing.T) {
	expect := QueryExpectation{
		MustInclude: []string{"a", "b", "c"},
	}
	actual := QueryResult{
		ReturnedIDs: []string{"a", "c"},
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{},
	}
	score := ScoreQuery(expect, actual)
	assert.InDelta(t, 2.0/3.0, score.Inclusion, 0.001)
	// Other dimensions have no expectations, so they score 1.0.
	assert.Equal(t, 1.0, score.Exclusion)
	assert.Equal(t, 1.0, score.Ranking)
	assert.Equal(t, 1.0, score.Conflict)
	assert.Equal(t, 1.0, score.Reach)
}

func TestScoreQuery_RankingWrong(t *testing.T) {
	expect := QueryExpectation{
		MustRankAbove: map[string]string{"a": "b"},
	}
	// b appears before a — ranking fails.
	actual := QueryResult{
		ReturnedIDs: []string{"b", "a"},
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 0.0, score.Ranking)
}

func TestScoreQuery_RankingMissing(t *testing.T) {
	expect := QueryExpectation{
		MustRankAbove: map[string]string{"a": "b"},
	}
	// a is present but b is missing — ranking cannot be satisfied.
	actual := QueryResult{
		ReturnedIDs: []string{"a"},
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 0.0, score.Ranking)
}

func TestScoreQuery_ConflictNotFlagged(t *testing.T) {
	expect := QueryExpectation{
		MustFlagConflict: []string{"a", "b"},
	}
	actual := QueryResult{
		ReturnedIDs: []string{"a", "b"},
		HasConflict: map[string]bool{"a": true, "b": false},
		ReachMethod: map[string]string{},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 0.5, score.Conflict)
}

func TestScoreQuery_ReachMismatch(t *testing.T) {
	expect := QueryExpectation{
		ExpectReach: map[string]string{"a": "direct", "b": "expansion"},
	}
	actual := QueryResult{
		ReturnedIDs: []string{"a", "b"},
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{"a": "direct", "b": "text"},
	}
	score := ScoreQuery(expect, actual)
	assert.Equal(t, 0.5, score.Reach)
}

func TestScoreQuery_EmptyExpectations(t *testing.T) {
	score := ScoreQuery(QueryExpectation{}, QueryResult{
		HasConflict: map[string]bool{},
		ReachMethod: map[string]string{},
	})
	assert.Equal(t, 1.0, score.Total)
}

func TestScoreScenario(t *testing.T) {
	scores := []QueryScore{
		{Total: 1.0},
		{Total: 0.5},
		{Total: 1.0},
	}
	ss := ScoreScenario("test", scores)
	assert.Equal(t, "test", ss.Name)
	assert.InDelta(t, 2.5/3.0, ss.Average, 0.001)
	assert.Equal(t, 2, ss.PassCount)
	assert.Equal(t, 3, ss.TotalCount)
}

func TestScoreScenario_Empty(t *testing.T) {
	ss := ScoreScenario("empty", nil)
	assert.Equal(t, 0.0, ss.Average)
	assert.Equal(t, 0, ss.TotalCount)
}

func TestComputeOverall(t *testing.T) {
	scenarios := []ScenarioScore{
		{Average: 0.8},
		{Average: 1.0},
		{Average: 0.6},
	}
	assert.InDelta(t, 0.8, ComputeOverall(scenarios), 0.001)
}

func TestComputeOverall_Empty(t *testing.T) {
	assert.Equal(t, 0.0, ComputeOverall(nil))
}

func TestComputeAblationLift(t *testing.T) {
	full := []ScenarioScore{{Average: 0.9}}
	without := []ScenarioScore{{Average: 0.7}}
	assert.InDelta(t, 0.2, ComputeAblationLift(full, without), 0.001)
}

func TestFormatReport_NoErrors(t *testing.T) {
	report := BenchmarkReport{
		Scenarios: []ScenarioScore{
			{Name: "A: Test", Average: 0.95, PassCount: 3, TotalCount: 3},
		},
		Overall: 0.95,
	}
	var buf bytes.Buffer
	FormatReport(report, &buf)
	out := buf.String()
	require.Contains(t, out, "KNOWN BENCHMARK RESULTS")
	require.Contains(t, out, "A: Test")
	require.Contains(t, out, "0.950")
	require.NotContains(t, out, "PER-QUERY FAILURES")
}

func TestFormatReport_WithFailures(t *testing.T) {
	report := BenchmarkReport{
		Scenarios: []ScenarioScore{
			{
				Name: "B: Fail",
				Average: 0.5,
				PassCount: 0,
				TotalCount: 1,
				Queries: []QueryScore{
					{QueryText: "bad query", Failures: []string{"missing: x"}},
				},
			},
		},
		Overall: 0.5,
	}
	var buf bytes.Buffer
	FormatReport(report, &buf)
	out := buf.String()
	require.Contains(t, out, "PER-QUERY FAILURES")
	require.Contains(t, out, "missing: x")
}

func TestFormatReport_WithAblation(t *testing.T) {
	report := BenchmarkReport{
		Scenarios: []ScenarioScore{
			{Name: "A", Average: 0.9, TotalCount: 1},
		},
		Overall: 0.9,
		Ablation: map[string]AblationResult{
			"Graph Expansion": {FullScore: 0.9, WithoutScore: 0.8, Lift: 0.1},
		},
	}
	var buf bytes.Buffer
	FormatReport(report, &buf)
	out := buf.String()
	require.Contains(t, out, "FEATURE ABLATION")
	require.Contains(t, out, "Graph Expansion")
	require.Contains(t, out, "+0.100")
}
