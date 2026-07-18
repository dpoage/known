// Package main is the capture-rate measurement harness for the known CLI.
//
// Usage:
//
//	go run ./bench/capture -bin /path/to/known
//
// Runs a scenario corpus derived from the friction-audit failure modes and
// prints per-scenario pass/fail + aggregate capture rate.  Results are also
// written as JSON when -out is set.
//
// The capture rate is (scenarios passing their predicate) / (total scenarios),
// where XFAIL scenarios count as failures in the denominator — they represent
// contracts the binary does not yet meet, so they honestly lower the score
// until the wave that fixes them lands.
package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	modelCacheFlag := flag.String("model-cache", "", "path to model cache dir (default: ~/.known/models)")
	flag.Parse()

	if *binFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: capture -bin /path/to/known [-out results.json] [-commit <hash>] [-model-cache <dir>]")
		os.Exit(2)
	}

	bin, err := filepath.Abs(*binFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bin path error: %v\n", err)
		os.Exit(2)
	}

	// Resolve shared model cache directory (avoids per-scenario network downloads).
	modelCache := *modelCacheFlag
	if modelCache == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			modelCache = filepath.Join(home, ".known", "models")
		}
	}
	if info, err := os.Stat(modelCache); err == nil && info.IsDir() {
		sharedModelDir = modelCache
		fmt.Printf("Model cache: %s\n", sharedModelDir)
	} else {
		fmt.Fprintf(os.Stderr, "warning: model cache not found at %s — embedder will re-download per scenario\n", modelCache)
	}

	scenarios := corpus()
	corpusHash := computeCorpusHash(scenarios)

	fmt.Printf("Binary:      %s\n", bin)
	if *commitFlag != "" {
		fmt.Printf("Commit:      %s\n", *commitFlag)
	}
	fmt.Printf("Corpus:      %d scenarios (hash: %s)\n\n", len(scenarios), corpusHash[:12])

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
				lines := strings.SplitN(strings.TrimRight(r.Output, "\n"), "\n", 5)
				for _, l := range lines {
					fmt.Printf("         > %s\n", l)
				}
			}
		}
	}

	// Compute capture rate.
	//
	// Denominator: ALL scenarios.  xfail scenarios count as failures because
	// they represent contracts not yet met — excluding them would pin the rate
	// at 100% before every wave and make improvement unmeasurable.
	//
	// Numerator: scenarios that PASS their predicate (including unexpected xpass).
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

	total := len(scenarios)
	numerator := pass + xpass
	rate := 0.0
	if total > 0 {
		rate = float64(numerator) / float64(total) * 100
	}

	fmt.Printf("\n--- Capture Rate ---\n")
	fmt.Printf("Pass:  %d/%d (%.0f%%)\n", numerator, total, rate)
	if xfail > 0 {
		fmt.Printf("XFail: %d (expected — wave-2 contracts not yet implemented)\n", xfail)
	}
	if xpass > 0 {
		fmt.Printf("XPass: %d (unexpected pass — wave may have landed)\n", xpass)
	}
	if fail > 0 {
		fmt.Printf("Fail:  %d unexpected\n", fail)
	}

	run := Run{
		BinPath:        bin,
		Commit:         *commitFlag,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		CorpusHash:     corpusHash,
		Pass:           numerator,
		Total:          total,
		XFail:          xfail,
		CaptureRatePct: rate,
		Scenarios:      results,
	}

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

// computeCorpusHash returns a short content-stable identifier for the scenario set.
// It hashes scenario IDs and AuditMode fields so the hash changes when scenarios
// are added, removed, or their predicates are retargeted.
func computeCorpusHash(scenarios []Scenario) string {
	h := sha256.New()
	for _, sc := range scenarios {
		fmt.Fprintf(h, "%s|%s|%v\n", sc.ID, sc.AuditMode, sc.ExpectFailBaseline)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Run is the top-level JSON output.
type Run struct {
	BinPath        string           `json:"bin"`
	Commit         string           `json:"commit,omitempty"`
	Timestamp      string           `json:"timestamp"`
	CorpusHash     string           `json:"corpus_hash"`
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
