//go:build bench

package bench

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatReport writes the full benchmark report to w.
func FormatReport(report BenchmarkReport, w io.Writer) {
	FormatScenarioTable(report.Scenarios, w)
	fmt.Fprintf(w, "\nOVERALL: %.3f\n", report.Overall)

	if len(report.Ablation) > 0 {
		fmt.Fprintln(w)
		FormatAblationTable(report.Ablation, w)
	}

	FormatFailures(report.Scenarios, w)
}

// FormatScenarioTable writes the scenario results table.
func FormatScenarioTable(scenarios []ScenarioScore, w io.Writer) {
	fmt.Fprintln(w, "=== KNOWN BENCHMARK RESULTS ===")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-40s %6s  %s\n", "Scenario", "Score", "Queries")
	fmt.Fprintln(w, strings.Repeat("─", 60))
	for _, s := range scenarios {
		fmt.Fprintf(w, "%-40s %6.3f  %d/%d\n",
			s.Name, s.Average, s.PassCount, s.TotalCount)
	}
}

// FormatAblationTable writes the feature ablation results.
func FormatAblationTable(ablation map[string]AblationResult, w io.Writer) {
	fmt.Fprintln(w, "=== FEATURE ABLATION ===")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-24s %6s  %7s  %s\n", "Feature", "Full", "Without", "Lift")
	fmt.Fprintln(w, strings.Repeat("─", 52))

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(ablation))
	for k := range ablation {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		r := ablation[name]
		fmt.Fprintf(w, "%-24s %6.3f  %7.3f  %+.3f\n",
			name, r.FullScore, r.WithoutScore, r.Lift)
	}
}

// FormatFailures writes per-query failure details.
func FormatFailures(scenarios []ScenarioScore, w io.Writer) {
	var hasFailures bool
	for _, s := range scenarios {
		for _, q := range s.Queries {
			if len(q.Failures) > 0 {
				hasFailures = true
				break
			}
		}
		if hasFailures {
			break
		}
	}
	if !hasFailures {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "=== PER-QUERY FAILURES ===")
	for _, s := range scenarios {
		for i, q := range s.Queries {
			if len(q.Failures) == 0 {
				continue
			}
			prefix := fmt.Sprintf("[%s.%d]", s.Name, i+1)
			fmt.Fprintf(w, "%s %q — %s\n", prefix, q.QueryText, strings.Join(q.Failures, "; "))
		}
	}
}
