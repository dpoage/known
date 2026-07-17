// Package main is the capture-rate measurement harness for the known CLI.
//
// Usage:
//
//	go run ./bench/capture -bin /path/to/known
//
// Runs a scenario corpus derived from the friction-audit failure modes and
// prints per-scenario pass/fail + aggregate capture rate.  Results are also
// written as JSON to bench/capture/results/<commit>.json when -out is set.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	binFlag := flag.String("bin", "", "path to known binary under test (required)")
	outFlag := flag.String("out", "", "write JSON results to this file (optional)")
	commitFlag := flag.String("commit", "", "commit hash to record in results")
	flag.Parse()

	if *binFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: capture -bin /path/to/known [-out results.json] [-commit <hash>]")
		os.Exit(2)
	}

	bin, err := filepath.Abs(*binFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bin path error: %v\n", err)
		os.Exit(2)
	}

	run := Run{
		BinPath:   bin,
		Commit:    *commitFlag,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	scenarios := corpus()
	results := make([]ScenarioResult, 0, len(scenarios))

	for _, sc := range scenarios {
		r := runScenario(bin, sc)
		results = append(results, r)
		status := "PASS"
		if !r.Pass {
			if sc.ExpectFailBaseline {
				status = "XFAIL"
			} else {
				status = "FAIL"
			}
		} else if sc.ExpectFailBaseline {
			status = "XPASS"
		}
		fmt.Printf("%-8s [%s] %s\n", status, sc.ID, sc.Name)
		if !r.Pass {
			fmt.Printf("         predicate: %s\n", r.PredicateDesc)
			if r.Output != "" {
				lines := strings.SplitN(strings.TrimRight(r.Output, "\n"), "\n", 4)
				for _, l := range lines {
					fmt.Printf("         > %s\n", l)
				}
			}
		}
	}

	pass, xfail, fail, xpass := 0, 0, 0, 0
	for i, r := range results {
		sc := scenarios[i]
		switch {
		case r.Pass && !sc.ExpectFailBaseline:
			pass++
		case r.Pass && sc.ExpectFailBaseline:
			xpass++
		case !r.Pass && sc.ExpectFailBaseline:
			xfail++
		default:
			fail++
		}
	}

	// Capture rate = scenarios that pass their predicate / total non-expected-fail scenarios.
	// xfail scenarios contribute 0; xpass count as pass.
	denominator := pass + fail + xpass
	numerator := pass + xpass
	rate := 0.0
	if denominator > 0 {
		rate = float64(numerator) / float64(denominator) * 100
	}

	fmt.Printf("\n--- Capture Rate ---\n")
	fmt.Printf("Pass:  %d/%d (%.0f%%)\n", numerator, denominator, rate)
	if xfail > 0 {
		fmt.Printf("XFail: %d (expected failures — wave-2 features not yet landed)\n", xfail)
	}
	if fail > 0 {
		fmt.Printf("Fail:  %d unexpected\n", fail)
	}

	run.Scenarios = results
	run.Pass = numerator
	run.Total = denominator
	run.XFail = xfail
	run.CaptureRatePct = rate

	if *outFlag != "" {
		if err := writeJSON(*outFlag, run); err != nil {
			fmt.Fprintf(os.Stderr, "write json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Results written to %s\n", *outFlag)
	}

	if fail > 0 {
		os.Exit(1)
	}
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Run is the top-level JSON output.
type Run struct {
	BinPath        string           `json:"bin"`
	Commit         string           `json:"commit,omitempty"`
	Timestamp      string           `json:"timestamp"`
	Pass           int              `json:"pass"`
	Total          int              `json:"total"`
	XFail          int              `json:"xfail"`
	CaptureRatePct float64          `json:"capture_rate_pct"`
	Scenarios      []ScenarioResult `json:"scenarios"`
}

// ScenarioResult holds the outcome for a single scenario.
type ScenarioResult struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Pass          bool   `json:"pass"`
	ExitCode      int    `json:"exit_code"`
	Output        string `json:"output"`
	PredicateDesc string `json:"predicate"`
	Error         string `json:"error,omitempty"`
}

// reULID matches a 26-character Crockford base32 ULID.
var reULID = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)

// reULIDJSON matches a ULID as a JSON string value (e.g. "id":"01KT...").
var reULIDJSON = regexp.MustCompile(`"id"\s*:\s*"[0-9A-HJKMNP-TV-Z]{26}"`)
