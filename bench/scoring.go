//go:build bench

// Package bench provides scoring, metrics, and report formatting for the
// known benchmark suite.
package bench

import "fmt"

// Weight constants for the per-query scoring formula.
var (
	WeightInclusion = 0.35
	WeightExclusion = 0.15
	WeightRanking   = 0.20
	WeightConflict  = 0.15
	WeightReach     = 0.15
)

// QueryExpectation defines expected outcomes for a single query.
type QueryExpectation struct {
	QueryText        string
	MustInclude      []string          // Entry IDs that MUST appear in results
	MustExclude      []string          // Entry IDs that MUST NOT appear
	MustRankAbove    map[string]string // key must rank above value in results
	MustFlagConflict []string          // Entry IDs that must have HasConflict=true
	ExpectReach      map[string]string // Entry ID -> expected ReachMethod
}

// QueryResult captures actual results for scoring.
type QueryResult struct {
	ReturnedIDs []string          // Entry IDs in result order
	HasConflict map[string]bool   // Entry ID -> HasConflict flag
	ReachMethod map[string]string // Entry ID -> ReachMethod string
}

// QueryScore holds the breakdown of a single query's score.
type QueryScore struct {
	QueryText string
	Total     float64
	Inclusion float64
	Exclusion float64
	Ranking   float64
	Conflict  float64
	Reach     float64
	Failures  []string
}

// ScenarioScore aggregates query scores for a scenario.
type ScenarioScore struct {
	Name       string
	Queries    []QueryScore
	Average    float64
	PassCount  int
	TotalCount int
}

// BenchmarkReport is the full benchmark result.
type BenchmarkReport struct {
	Scenarios []ScenarioScore
	Overall   float64
	Ablation  map[string]AblationResult
}

// AblationResult captures the effect of disabling a feature.
type AblationResult struct {
	FullScore    float64
	WithoutScore float64
	Lift         float64
}

// ScoreQuery evaluates a single query's results against expectations.
func ScoreQuery(expect QueryExpectation, actual QueryResult) QueryScore {
	qs := QueryScore{QueryText: expect.QueryText}

	returnedSet := make(map[string]int, len(actual.ReturnedIDs))
	for i, id := range actual.ReturnedIDs {
		returnedSet[id] = i
	}

	// Inclusion: fraction of must_include found in results.
	if len(expect.MustInclude) > 0 {
		found := 0
		for _, id := range expect.MustInclude {
			if _, ok := returnedSet[id]; ok {
				found++
			} else {
				qs.Failures = append(qs.Failures, fmt.Sprintf("missing: %s", id))
			}
		}
		qs.Inclusion = float64(found) / float64(len(expect.MustInclude))
	} else {
		qs.Inclusion = 1.0
	}

	// Exclusion: fraction of must_exclude absent from results.
	if len(expect.MustExclude) > 0 {
		absent := 0
		for _, id := range expect.MustExclude {
			if _, ok := returnedSet[id]; !ok {
				absent++
			} else {
				qs.Failures = append(qs.Failures, fmt.Sprintf("should not appear: %s", id))
			}
		}
		qs.Exclusion = float64(absent) / float64(len(expect.MustExclude))
	} else {
		qs.Exclusion = 1.0
	}

	// Ranking: fraction of must_rank_above pairs satisfied.
	if len(expect.MustRankAbove) > 0 {
		satisfied := 0
		for higher, lower := range expect.MustRankAbove {
			hiIdx, hiOK := returnedSet[higher]
			loIdx, loOK := returnedSet[lower]
			if hiOK && loOK && hiIdx < loIdx {
				satisfied++
			} else {
				qs.Failures = append(qs.Failures, fmt.Sprintf("%s should rank above %s", higher, lower))
			}
		}
		qs.Ranking = float64(satisfied) / float64(len(expect.MustRankAbove))
	} else {
		qs.Ranking = 1.0
	}

	// Conflict: fraction of must_flag_conflict entries correctly flagged.
	if len(expect.MustFlagConflict) > 0 {
		flagged := 0
		for _, id := range expect.MustFlagConflict {
			if actual.HasConflict[id] {
				flagged++
			} else {
				qs.Failures = append(qs.Failures, fmt.Sprintf("conflict not flagged: %s", id))
			}
		}
		qs.Conflict = float64(flagged) / float64(len(expect.MustFlagConflict))
	} else {
		qs.Conflict = 1.0
	}

	// Reach: fraction of expect_reach entries with correct ReachMethod.
	if len(expect.ExpectReach) > 0 {
		matched := 0
		for id, expectedMethod := range expect.ExpectReach {
			if actual.ReachMethod[id] == expectedMethod {
				matched++
			} else {
				qs.Failures = append(qs.Failures,
					fmt.Sprintf("reach mismatch for %s: want %s, got %s", id, expectedMethod, actual.ReachMethod[id]))
			}
		}
		qs.Reach = float64(matched) / float64(len(expect.ExpectReach))
	} else {
		qs.Reach = 1.0
	}

	qs.Total = WeightInclusion*qs.Inclusion +
		WeightExclusion*qs.Exclusion +
		WeightRanking*qs.Ranking +
		WeightConflict*qs.Conflict +
		WeightReach*qs.Reach

	return qs
}

// ScoreScenario aggregates query scores into a scenario score.
func ScoreScenario(name string, scores []QueryScore) ScenarioScore {
	ss := ScenarioScore{
		Name:       name,
		Queries:    scores,
		TotalCount: len(scores),
	}
	if len(scores) == 0 {
		return ss
	}
	sum := 0.0
	for _, qs := range scores {
		sum += qs.Total
		if qs.Total == 1.0 {
			ss.PassCount++
		}
	}
	ss.Average = sum / float64(len(scores))
	return ss
}

// ComputeOverall computes the mean of scenario averages.
func ComputeOverall(scenarios []ScenarioScore) float64 {
	if len(scenarios) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range scenarios {
		sum += s.Average
	}
	return sum / float64(len(scenarios))
}

// ComputeAblationLift computes the score difference between full and ablated runs.
func ComputeAblationLift(full, without []ScenarioScore) float64 {
	return ComputeOverall(full) - ComputeOverall(without)
}
