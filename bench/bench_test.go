//go:build bench

package bench

import (
	"bytes"
	"context"
	"os"
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

// seedDBPath returns the absolute path to the seed database.
func seedDBPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "seed.db")
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
	if cfg.DisableScoping {
		sq.Scope = ""
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
			Vector:      vecOpts,
			ExpandDepth: sq.ExpandDepth,
			TextSearch:  true,
		})
	case sq.TextSearch:
		// Hybrid with text search, no expansion (still uses RRF fusion).
		results, err = eng.SearchHybrid(ctx, query.HybridOptions{
			Vector:      vecOpts,
			ExpandDepth: 1,
			TextSearch:  true,
		})
	case sq.ExpandDepth > 0:
		// Hybrid with graph expansion, no text search.
		results, err = eng.SearchHybrid(ctx, query.HybridOptions{
			Vector:      vecOpts,
			ExpandDepth: sq.ExpandDepth,
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

func TestBench(t *testing.T) {
	ctx := context.Background()

	// 1. Verify seed database exists.
	dbPath := seedDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("Seed database not found at %s. Run seedgen first:\n"+
			"  go run bench/cmd/seedgen/main.go", dbPath)
	}

	// 2. Open a read-only copy to avoid mutating the seed.
	// Copy the seed DB to a temp file so SQLite WAL mode doesn't create
	// side files next to the original.
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
	defer db.Close()

	// 3. Create the hugot embedder (same model as seedgen).
	cfg := embed.Config{
		Embedder:     "hugot",
		Model:        "sentence-transformers/all-MiniLM-L6-v2",
		CacheEnabled: true,
	}
	embedder, err := embed.NewEmbedder(cfg)
	if err != nil {
		t.Fatalf("Create embedder: %v", err)
	}

	// 4. Create query engine.
	eng := query.New(db.Entries(), db.Edges(), embedder)

	// 5. Load all entries and build content index.
	allEntries, err := db.Entries().List(ctx, storage.EntryFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("List entries: %v", err)
	}
	t.Logf("Loaded %d entries from seed database", len(allEntries))

	ci := &contentIndex{entries: allEntries}

	// 6. Run all scenarios (full run).
	scenarios := AllScenarios()
	var scenarioScores []ScenarioScore

	t.Run("Full", func(t *testing.T) {
		scenarioScores = runScenarios(ctx, t, eng, ci, scenarios, nil)
	})

	// 7. Run ablation tests — rerun each scenario with one feature disabled.
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

	// 8. Compute overall score and format report.
	report := BenchmarkReport{
		Scenarios: scenarioScores,
		Overall:   fullOverall,
		Ablation:  ablationResults,
	}

	var buf bytes.Buffer
	FormatReport(report, &buf)
	t.Logf("\n%s", buf.String())

	// 9. Fail if overall is below minimum threshold.
	const minThreshold = 0.5
	if fullOverall < minThreshold {
		t.Errorf("Overall score %.3f is below minimum threshold %.3f", fullOverall, minThreshold)
	}
}
