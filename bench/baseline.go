//go:build bench

package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// Regression thresholds — scores that drop by more than these amounts are flagged.
var (
	RetrievalRegressionThreshold     = 0.02 // 2 percentage points
	EffectivenessRegressionThreshold = 0.05 // 5 percentage points
)

// Baseline stores benchmark scores for regression detection.
type Baseline struct {
	// Retrieval scenario scores.
	Scenarios map[string]float64 `json:"scenarios"`
	Overall   float64            `json:"overall"`

	// Ablation lift per feature.
	Ablation map[string]float64 `json:"ablation,omitempty"`

	// Effectiveness scores per condition per session.
	Effectiveness map[string]map[int]float64 `json:"effectiveness,omitempty"`
	// Effectiveness overall per condition.
	EffectivenessOverall map[string]float64 `json:"effectiveness_overall,omitempty"`
}

// Regression describes a single detected regression.
type Regression struct {
	Area     string  // e.g., "scenario:A: Codebase Discovery", "ablation:Graph Expansion", "effectiveness:with_memory:session:3"
	Baseline float64 // previous score
	Current  float64 // new score
	Delta    float64 // negative means regression
}

// BaselineFromReport builds a Baseline from a BenchmarkReport.
func BaselineFromReport(report BenchmarkReport) Baseline {
	b := Baseline{
		Scenarios: make(map[string]float64, len(report.Scenarios)),
		Overall:   report.Overall,
	}
	for _, s := range report.Scenarios {
		b.Scenarios[s.Name] = s.Average
	}
	if len(report.Ablation) > 0 {
		b.Ablation = make(map[string]float64, len(report.Ablation))
		for name, ar := range report.Ablation {
			b.Ablation[name] = ar.Lift
		}
	}
	return b
}

// AddEffectiveness merges effectiveness results into the baseline.
func (b *Baseline) AddEffectiveness(report *EffectivenessReport) {
	if report == nil {
		return
	}
	b.Effectiveness = make(map[string]map[int]float64)
	b.EffectivenessOverall = make(map[string]float64)
	for cond, result := range report.Results {
		key := string(cond)
		b.Effectiveness[key] = make(map[int]float64)
		for session, score := range result.SessionScores {
			b.Effectiveness[key][session] = score
		}
		b.EffectivenessOverall[key] = result.OverallScore
	}
}

// SaveBaseline writes a baseline to a JSON file.
func SaveBaseline(path string, b Baseline) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadBaseline reads a baseline from a JSON file. Returns nil if the file
// does not exist.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("unmarshal baseline: %w", err)
	}
	return &b, nil
}

// CompareBaseline checks for regressions between an old baseline and current results.
func CompareBaseline(old Baseline, current Baseline) []Regression {
	var regs []Regression

	// Retrieval scenario scores.
	for name, oldScore := range old.Scenarios {
		if curScore, ok := current.Scenarios[name]; ok {
			delta := curScore - oldScore
			if delta < -RetrievalRegressionThreshold {
				regs = append(regs, Regression{
					Area:     "scenario:" + name,
					Baseline: oldScore,
					Current:  curScore,
					Delta:    delta,
				})
			}
		}
	}

	// Overall retrieval.
	if delta := current.Overall - old.Overall; delta < -RetrievalRegressionThreshold {
		regs = append(regs, Regression{
			Area:     "overall",
			Baseline: old.Overall,
			Current:  current.Overall,
			Delta:    delta,
		})
	}

	// Ablation lift.
	for name, oldLift := range old.Ablation {
		if curLift, ok := current.Ablation[name]; ok {
			delta := curLift - oldLift
			if delta < -RetrievalRegressionThreshold {
				regs = append(regs, Regression{
					Area:     "ablation:" + name,
					Baseline: oldLift,
					Current:  curLift,
					Delta:    delta,
				})
			}
		}
	}

	// Effectiveness overall.
	for cond, oldScore := range old.EffectivenessOverall {
		if curScore, ok := current.EffectivenessOverall[cond]; ok {
			delta := curScore - oldScore
			if delta < -EffectivenessRegressionThreshold {
				regs = append(regs, Regression{
					Area:     "effectiveness:" + cond,
					Baseline: oldScore,
					Current:  curScore,
					Delta:    delta,
				})
			}
		}
	}

	// Sort for deterministic output.
	sort.Slice(regs, func(i, j int) bool {
		return regs[i].Area < regs[j].Area
	})
	return regs
}

// FormatRegressions writes regression warnings to w. Returns the count.
func FormatRegressions(regs []Regression, w io.Writer) int {
	if len(regs) == 0 {
		fmt.Fprintln(w, "\n=== REGRESSION CHECK: PASS ===")
		fmt.Fprintln(w, "No regressions detected against baseline.")
		return 0
	}
	fmt.Fprintln(w, "\n=== REGRESSION CHECK: FAIL ===")
	fmt.Fprintf(w, "%d regression(s) detected:\n\n", len(regs))
	for _, r := range regs {
		fmt.Fprintf(w, "  %-50s  baseline=%.3f  current=%.3f  delta=%.3f\n",
			r.Area, r.Baseline, r.Current, r.Delta)
	}
	return len(regs)
}

// round3 rounds to 3 decimal places for stable JSON output.
func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}
