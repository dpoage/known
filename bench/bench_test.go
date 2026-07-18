//go:build bench

package bench

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/sqlite"
)

// benchDir returns the absolute path to the bench/ directory.
func benchDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Dir(thisFile)
}

// generateSeedDB runs the seedgen program to create the seed database.
func generateSeedDB(t *testing.T) error {
	t.Helper()
	seedgenPath := filepath.Join(benchDir(), "cmd", "seedgen", "main.go")
	cmd := exec.Command("go", "run", seedgenPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// contentIndex maps content substrings to entry IDs for resolving
// scenario expectations that reference entries by content.
type contentIndex struct {
	entries []model.Entry
}

// findID returns the ID of the first entry whose content contains substr.
// Returns an empty string if no match is found.
func (ci *contentIndex) findID(substr string) string {
	lower := strings.ToLower(substr)
	for _, e := range ci.entries {
		if strings.Contains(strings.ToLower(e.Content), lower) {
			return e.ID.String()
		}
	}
	return ""
}

// resolveExpectation converts a ScenarioQuery (content-based references) into
// a QueryExpectation (ID-based references) using the content index.
func resolveExpectation(sq ScenarioQuery, ci *contentIndex, t *testing.T) QueryExpectation {
	t.Helper()
	qe := QueryExpectation{
		QueryText: sq.Text,
	}

	for _, substr := range sq.MustIncludeContent {
		id := ci.findID(substr)
		if id == "" {
			t.Errorf("MustInclude: no entry found matching %q", substr)
			continue
		}
		qe.MustInclude = append(qe.MustInclude, id)
	}

	for _, substr := range sq.MustExcludeContent {
		id := ci.findID(substr)
		if id == "" {
			// Not finding an entry to exclude is fine — it means the content
			// does not exist in the DB, which satisfies exclusion trivially.
			continue
		}
		qe.MustExclude = append(qe.MustExclude, id)
	}

	if len(sq.MustRankAboveContent) > 0 {
		qe.MustRankAbove = make(map[string]string, len(sq.MustRankAboveContent))
		for higherSubstr, lowerSubstr := range sq.MustRankAboveContent {
			higherID := ci.findID(higherSubstr)
			lowerID := ci.findID(lowerSubstr)
			if higherID == "" {
				t.Errorf("MustRankAbove: no entry found for higher %q", higherSubstr)
				continue
			}
			if lowerID == "" {
				t.Errorf("MustRankAbove: no entry found for lower %q", lowerSubstr)
				continue
			}
			qe.MustRankAbove[higherID] = lowerID
		}
	}

	for _, substr := range sq.MustFlagConflictContent {
		id := ci.findID(substr)
		if id == "" {
			t.Errorf("MustFlagConflict: no entry found matching %q", substr)
			continue
		}
		qe.MustFlagConflict = append(qe.MustFlagConflict, id)
	}

	if len(sq.ExpectReachContent) > 0 {
		qe.ExpectReach = make(map[string]string, len(sq.ExpectReachContent))
		for substr, method := range sq.ExpectReachContent {
			id := ci.findID(substr)
			if id == "" {
				t.Errorf("ExpectReach: no entry found matching %q", substr)
				continue
			}
			qe.ExpectReach[id] = method
		}
	}

	return qe
}

// applyAblation returns a copy of the ScenarioQuery with the ablation
// config's overrides applied. The original is never mutated.
func applyAblation(sq ScenarioQuery, cfg *AblationConfig) ScenarioQuery {
	if cfg == nil {
		return sq
	}
	if cfg.DisableGraphExpansion {
		sq.ExpandDepth = 0
	}
	if cfg.DisableTextSearch {
		sq.TextSearch = false
	}
	if cfg.DisableFreshness {
		sq.Recency = 0
	}
	return sq
}

// executeQuery runs a single scenario query against the engine and returns
// a QueryResult with IDs, conflict flags, and reach methods.
func executeQuery(ctx context.Context, eng *query.Engine, sq ScenarioQuery, ci *contentIndex) (QueryResult, error) {
	vecOpts := query.VectorOptions{
		Text:          sq.Text,
		Scope:         sq.Scope,
		Limit:         sq.Limit,
		Threshold:     sq.Threshold,
		RecencyWeight: sq.Recency,
	}

	var results []query.Result
	var err error

	switch {
	case sq.TextSearch && sq.ExpandDepth > 0:
		// Hybrid with text search and graph expansion.
		results, err = eng.SearchHybrid(ctx, query.HybridOptions{
			Vector:            vecOpts,
			ExpandDepth:       sq.ExpandDepth,
			TextSearch:        true,
			IncludeSuperseded: sq.IncludeSuperseded,
		})
	case sq.TextSearch:
		// Hybrid with text search, no expansion (still uses RRF fusion).
		results, err = eng.SearchHybrid(ctx, query.HybridOptions{
			Vector:            vecOpts,
			ExpandDepth:       1,
			TextSearch:        true,
			IncludeSuperseded: sq.IncludeSuperseded,
		})
	case sq.ExpandDepth > 0:
		// Hybrid with graph expansion, no text search.
		results, err = eng.SearchHybrid(ctx, query.HybridOptions{
			Vector:            vecOpts,
			ExpandDepth:       sq.ExpandDepth,
			IncludeSuperseded: sq.IncludeSuperseded,
		})
	default:
		// Pure vector search.
		results, err = eng.SearchVector(ctx, vecOpts)
	}

	if err != nil {
		return QueryResult{}, err
	}

	// Post-filter by provenance if requested.
	if sq.Provenance != "" {
		prov := model.ProvenanceLevel(sq.Provenance)
		filtered := results[:0]
		for _, r := range results {
			if r.Entry.Provenance.Level == prov {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	return toQueryResult(results), nil
}

// runScenarios executes all scenarios and returns per-scenario scores. When
// ablation is non-nil, each query is copied and the ablation overrides are
// applied before execution.
func runScenarios(
	ctx context.Context,
	t *testing.T,
	eng *query.Engine,
	ci *contentIndex,
	scenarios []Scenario,
	ablation *AblationConfig,
) []ScenarioScore {
	var scores []ScenarioScore

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			var queryScores []QueryScore

			for i, sq := range scenario.Queries {
				// Apply ablation overrides to a copy.
				sq = applyAblation(sq, ablation)

				// Resolve content-based expectations to ID-based.
				expect := resolveExpectation(sq, ci, t)

				// Execute the query.
				actual, err := executeQuery(ctx, eng, sq, ci)
				if err != nil {
					t.Errorf("Query %d (%q): %v", i+1, sq.Text, err)
					queryScores = append(queryScores, QueryScore{
						QueryText: sq.Text,
						Failures:  []string{"query error: " + err.Error()},
					})
					continue
				}

				// Score.
				qs := ScoreQuery(expect, actual)
				queryScores = append(queryScores, qs)

				if len(qs.Failures) > 0 {
					t.Logf("  Query %d (%q) score=%.3f failures: %s",
						i+1, sq.Text, qs.Total, strings.Join(qs.Failures, "; "))
				} else {
					t.Logf("  Query %d (%q) score=%.3f", i+1, sq.Text, qs.Total)
				}
			}

			ss := ScoreScenario(scenario.Name, queryScores)
			scores = append(scores, ss)
			t.Logf("Scenario score: %.3f (%d/%d perfect)", ss.Average, ss.PassCount, ss.TotalCount)
		})
	}

	return scores
}

// toQueryResult converts engine results into the scoring QueryResult type.
func toQueryResult(results []query.Result) QueryResult {
	qr := QueryResult{
		ReturnedIDs: make([]string, len(results)),
		HasConflict: make(map[string]bool, len(results)),
		ReachMethod: make(map[string]string, len(results)),
	}
	for i, r := range results {
		id := r.Entry.ID.String()
		qr.ReturnedIDs[i] = id
		qr.HasConflict[id] = r.HasConflict
		qr.ReachMethod[id] = string(r.Reach)
	}
	return qr
}

// requireBenchFull skips t unless KNOWN_BENCH_FULL=1 is set. Hermetic
// contract: plain `go test -tags bench ./bench/...` (no network, no model
// cache, no API keys) must pass. Any test needing the real hugot embedder or
// a generated seed DB must call this first, so it skips by default and only
// runs when a human/CI opts in with KNOWN_BENCH_FULL=1.
func requireBenchFull(t *testing.T) {
	t.Helper()
	if os.Getenv("KNOWN_BENCH_FULL") != "1" {
		t.Skip("requires the real hugot embedder and a generated seed DB; set KNOWN_BENCH_FULL=1 to run")
	}
}

// openBenchEngine generates the seed database if it doesn't exist, opens a
// scratch copy (so SQLite WAL side files never touch the checked-in seed),
// and returns a ready query engine plus its content index. Callers must call
// requireBenchFull first.
func openBenchEngine(t *testing.T) (*query.Engine, *contentIndex) {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(benchDir(), "testdata", "seed.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Logf("Seed database not found at %s — generating...", dbPath)
		if err := generateSeedDB(t); err != nil {
			t.Fatalf("Failed to generate seed database: %v", err)
		}
	}

	tmpDir := t.TempDir()
	tmpDB := filepath.Join(tmpDir, "seed.db")
	seedBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("Read seed DB: %v", err)
	}
	if err := os.WriteFile(tmpDB, seedBytes, 0o644); err != nil {
		t.Fatalf("Write temp DB: %v", err)
	}

	db, err := sqlite.New(ctx, tmpDB)
	if err != nil {
		t.Fatalf("Open seed DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := embed.Config{
		Embedder:     "hugot",
		Model:        "sentence-transformers/all-MiniLM-L6-v2",
		CacheEnabled: true,
	}
	embedder, err := embed.NewEmbedder(cfg)
	if err != nil {
		t.Fatalf("Create embedder: %v", err)
	}

	eng := query.New(db.Entries(), db.Edges(), embedder)

	allEntries, err := db.Entries().List(ctx, storage.EntryFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("List entries: %v", err)
	}
	t.Logf("Loaded %d entries from seed database", len(allEntries))

	return eng, &contentIndex{entries: allEntries}
}

func TestBench(t *testing.T) {
	requireBenchFull(t)
	ctx := context.Background()
	eng, ci := openBenchEngine(t)

	// Run all scenarios (full run).
	scenarios := AllScenarios()
	var scenarioScores []ScenarioScore

	t.Run("Full", func(t *testing.T) {
		scenarioScores = runScenarios(ctx, t, eng, ci, scenarios, nil)
	})

	// Run ablation tests — rerun each scenario with one feature disabled.
	fullOverall := ComputeOverall(scenarioScores)
	ablationResults := make(map[string]AblationResult, len(DefaultAblations()))

	for _, cfg := range DefaultAblations() {
		cfg := cfg // capture range variable
		t.Run("Ablation/"+cfg.Name, func(t *testing.T) {
			ablatedScores := runScenarios(ctx, t, eng, ci, scenarios, &cfg)
			withoutOverall := ComputeOverall(ablatedScores)
			ablationResults[cfg.Name] = AblationResult{
				FullScore:    fullOverall,
				WithoutScore: withoutOverall,
				Lift:         fullOverall - withoutOverall,
			}
			t.Logf("Ablation %q: full=%.3f without=%.3f lift=%+.3f",
				cfg.Name, fullOverall, withoutOverall, fullOverall-withoutOverall)
		})
	}

	// Compute overall score and format report.
	report := BenchmarkReport{
		Scenarios: scenarioScores,
		Overall:   fullOverall,
		Ablation:  ablationResults,
	}

	var buf bytes.Buffer
	FormatReport(report, &buf)
	t.Logf("\n%s", buf.String())

	// Fail if overall is below minimum threshold.
	const minThreshold = 0.5
	if fullOverall < minThreshold {
		t.Errorf("Overall score %.3f is below minimum threshold %.3f", fullOverall, minThreshold)
	}
}

// TestBenchFalsification_AblationLiftsAreLoadBearing is the pipeline-level
// falsification companion to the predicate self-tests in scoring_test.go: it
// runs the real engine (not synthetic results) against a deliberately
// degraded config — ExpandDepth forced to 0, or TextSearch turned off — and
// asserts the overall score measurably drops. This is a regression guard on
// the corpus itself: the pre-known-58u suite measured Graph Expansion and
// FTS5 lifts of only ~0.013-0.018 (a saturated corpus barely notices either
// feature going away). The distractor-expanded corpus must do better.
func TestBenchFalsification_AblationLiftsAreLoadBearing(t *testing.T) {
	requireBenchFull(t)
	ctx := context.Background()
	eng, ci := openBenchEngine(t)

	scenarios := AllScenarios()
	full := runScenarios(ctx, t, eng, ci, scenarios, nil)
	fullOverall := ComputeOverall(full)

	const minLift = 0.02 // pre-known-58u baseline was ~0.013 (FTS5) / ~0.018 (Graph Expansion)
	for _, cfg := range DefaultAblations() {
		if cfg.Name == "Freshness Weighting" {
			continue // covered by TestBenchFalsification_FreshnessAblationFlipsRanking
		}
		cfg := cfg
		without := runScenarios(ctx, t, eng, ci, scenarios, &cfg)
		lift := fullOverall - ComputeOverall(without)
		if lift < minLift {
			t.Errorf("ablation %q lift=%.3f is not measurably load-bearing (want >= %.3f)", cfg.Name, lift, minLift)
		} else {
			t.Logf("ablation %q lift=%.3f >= %.3f (load-bearing)", cfg.Name, lift, minLift)
		}
	}
}

// TestBenchFalsification_FreshnessAblationFlipsRanking proves scenario J's
// ranking assertion is load-bearing rather than accidental: the corpus pairs
// a stale entry with higher raw vector similarity against a current entry
// with lower raw similarity (see scenarioJ doc comment). With RecencyWeight
// forced to 0 (DefaultAblations' "Freshness Weighting" config), the ranking
// predicate must actually fail — proving known-oj3's ObservedAt-based
// freshness, not raw similarity, is what makes the full-config ranking
// correct.
func TestBenchFalsification_FreshnessAblationFlipsRanking(t *testing.T) {
	requireBenchFull(t)
	ctx := context.Background()
	eng, ci := openBenchEngine(t)

	scenarios := []Scenario{scenarioJ()}
	full := runScenarios(ctx, t, eng, ci, scenarios, nil)
	ablated := runScenarios(ctx, t, eng, ci, scenarios,
		&AblationConfig{Name: "Freshness Weighting", DisableFreshness: true})

	if full[0].Average != 1.0 {
		t.Fatalf("full scenario J average = %.3f, want 1.0 (freshness-driven ranking should hold)", full[0].Average)
	}
	if ablated[0].Average >= full[0].Average {
		t.Errorf("ablated (Recency=0) scenario J average = %.3f did not drop below full %.3f — freshness ranking is not load-bearing",
			ablated[0].Average, full[0].Average)
	} else {
		t.Logf("freshness ranking falsified as expected: full=%.3f ablated=%.3f", full[0].Average, ablated[0].Average)
	}
}

// TestSupersedeDemotion_Falsification proves the known-5oq demotion is
// exercised by scenario H's config (ExpandDepth>0, so SearchHybrid runs
// enrichSuperseded): bypassing it via IncludeSuperseded=true must not
// increase the rank-above/inclusion score relative to the default (demoted)
// run. Combined with the doc comment on scenarioH, this documents exactly
// how much headroom the demotion buys on this corpus.
func TestSupersedeDemotion_Falsification(t *testing.T) {
	requireBenchFull(t)
	ctx := context.Background()
	eng, ci := openBenchEngine(t)

	base := scenarioH().Queries[0]
	bypass := base
	bypass.IncludeSuperseded = true

	baseResult, err := executeQuery(ctx, eng, base, ci)
	if err != nil {
		t.Fatalf("executeQuery (default): %v", err)
	}
	bypassResult, err := executeQuery(ctx, eng, bypass, ci)
	if err != nil {
		t.Fatalf("executeQuery (bypass): %v", err)
	}

	baseScore := ScoreQuery(resolveExpectation(base, ci, t), baseResult)
	bypassScore := ScoreQuery(resolveExpectation(bypass, ci, t), bypassResult)

	if baseScore.Total != 1.0 {
		t.Fatalf("default (demoted) total score = %.3f, want 1.0", baseScore.Total)
	}
	t.Logf("default total=%.3f bypass total=%.3f", baseScore.Total, bypassScore.Total)

	// The bypass must not silently keep the successor ranked first for free:
	// find the successor and the superseded predecessor and confirm the
	// predecessor's rank climbs measurably once demotion is disabled.
	successorIdx := indexOfContent(baseResult.ReturnedIDs, bypassResult.ReturnedIDs, ci, "Server-Sent Events (SSE)")
	predecessorBaseIdx := indexOfContent(baseResult.ReturnedIDs, baseResult.ReturnedIDs, ci, "used polling every 30 seconds")
	predecessorBypassIdx := indexOfContent(bypassResult.ReturnedIDs, bypassResult.ReturnedIDs, ci, "used polling every 30 seconds")
	if successorIdx < 0 || predecessorBaseIdx < 0 || predecessorBypassIdx < 0 {
		t.Fatalf("expected entries not found in results (successor=%d base-pred=%d bypass-pred=%d)",
			successorIdx, predecessorBaseIdx, predecessorBypassIdx)
	}
	if predecessorBypassIdx >= predecessorBaseIdx {
		t.Errorf("IncludeSuperseded=true predecessor rank %d did not climb above the demoted rank %d — demotion is not load-bearing",
			predecessorBypassIdx, predecessorBaseIdx)
	} else {
		t.Logf("demotion falsified as expected: predecessor rank %d (demoted) -> %d (bypassed)", predecessorBaseIdx, predecessorBypassIdx)
	}
}

// indexOfContent returns the position of the first ID in ids whose entry
// content contains substr, using ci to resolve content. The unused first
// parameter keeps call sites self-documenting about which result set the
// caller intended; only the third positional slice is actually searched.
func indexOfContent(_ []string, ids []string, ci *contentIndex, substr string) int {
	targetID := ci.findID(substr)
	if targetID == "" {
		return -1
	}
	for i, id := range ids {
		if id == targetID {
			return i
		}
	}
	return -1
}
