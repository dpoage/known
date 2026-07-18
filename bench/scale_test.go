//go:build bench

package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage/sqlite"
)

// fakeEmbedDim is the vector width used by FakeEmbedder throughout this
// file. It only needs to be large enough to keep hash collisions rare
// across the workload's vocabulary; 128 is generous for ~1,300 fact
// sentences worth of distinct tokens.
const fakeEmbedDim = 128

// scaleSizes are the corpus sizes exercised by the latency benchmarks.
var scaleSizes = []int{1000, 5000, 10000}

// buildScaleCorpus creates a fresh in-memory SQLite database, generates a
// deterministic n-document corpus, and populates it via the storage API
// (not the CLI) using embedder. Returns a ready query engine, the corpus
// that was generated, and a doc-key -> storage ID map for translating
// LabeledQuery judgments.
func buildScaleCorpus(tb testing.TB, ctx context.Context, n int, embedder embed.Embedder) (*query.Engine, Corpus, map[string]model.ID) {
	tb.Helper()

	db, err := sqlite.New(ctx, ":memory:")
	if err != nil {
		tb.Fatalf("open db: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		tb.Fatalf("migrate: %v", err)
	}

	corpus := GenerateCorpus(n)
	ids, err := PopulateCorpus(ctx, db, corpus, embedder)
	if err != nil {
		tb.Fatalf("populate corpus: %v", err)
	}

	eng := query.New(db.Entries(), db.Edges(), embedder)
	return eng, corpus, ids
}

// resolveJudgments translates a LabeledQuery's doc-key-keyed judgments into
// ID-string-keyed Judgments using the doc key -> storage ID map returned by
// buildScaleCorpus/PopulateCorpus.
func resolveJudgments(byKey map[string]int, ids map[string]model.ID) Judgments {
	rel := make(Judgments, len(byKey))
	for key, grade := range byKey {
		if id, ok := ids[key]; ok {
			rel[id.String()] = grade
		}
	}
	return rel
}

func idsOf(results []query.Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Entry.ID.String()
	}
	return out
}

// reversed returns a new slice with ranked's elements in reverse order,
// leaving ranked untouched.
func reversed(ranked []string) []string {
	out := make([]string, len(ranked))
	for i, id := range ranked {
		out[len(ranked)-1-i] = id
	}
	return out
}

// =============================================================================
// Latency / allocation benchmarks
// =============================================================================

// BenchmarkSearchLatency measures query.Engine.SearchVector latency and
// allocations -- the same code path production search uses, down through
// storage.EntryRepo.SearchSimilar -- at 1K/5K/10K corpus scale. It runs
// against the deterministic FakeEmbedder so it needs no network, model
// cache, or API key: `go test -tags bench -bench Search ./bench/` runs all
// three scales hermetically (population itself, not just the timed search
// loop, is cheap with the hashing-trick embedder).
func BenchmarkSearchLatency(b *testing.B) {
	ctx := context.Background()
	fake := NewFakeEmbedder(fakeEmbedDim)

	for _, n := range scaleSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			eng, _, _ := buildScaleCorpus(b, ctx, n, fake)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				results, err := eng.SearchVector(ctx, query.VectorOptions{
					Text:  "connection pool exhausted during a traffic spike",
					Scope: "workload",
					Limit: 10,
				})
				if err != nil {
					b.Fatalf("SearchVector: %v", err)
				}
				if len(results) == 0 {
					b.Fatal("SearchVector returned no results")
				}
			}
		})
	}
}

// =============================================================================
// Quality evaluation
// =============================================================================

// runQualityEval evaluates P@5, MRR (via ReciprocalRank/MRR), and NDCG@10
// over every labeled query against a 1,000-document corpus embedded with
// embedder, then logs a metrics table. It fails if the mean NDCG@10
// saturates at (effectively) 1.0 -- a saturated metric proves nothing about
// discrimination -- or if mean P@5 is zero, which would indicate the
// workload/labels are broken rather than merely imperfect.
//
// The gate checks NDCG@10, not P@5/MRR, deliberately. Empirically (see
// known-5qr notes), a strong embedder (real hugot/MiniLM) drives P@5 and
// MRR to ~1.0 on these queries because every query is phrased as a direct,
// unambiguous paraphrase of one fact: a competent semantic search finding
// *a* relevant (fact-matching) document at rank 1 every time is correct
// behavior, not benchmark saturation. NDCG@10 stays well below 1.0 even
// then (0.918 mean against MiniLM, 0.464 mean against FakeEmbedder) because
// it is graded -- it still penalizes ranking a same-topic-diluted (grade 2)
// or merely-adjacent (grade 1) doc above a focused grade-3 answer. That
// graded sensitivity, not the binary top-1 metrics, is what this benchmark
// suite relies on for detecting regressions once a system is already
// "good enough" to nail P@5/MRR.
func runQualityEval(t *testing.T, ctx context.Context, embedder embed.Embedder, label string) {
	t.Helper()

	eng, corpus, ids := buildScaleCorpus(t, ctx, 1000, embedder)
	queries := BuildLabeledQueries(corpus)
	if len(queries) < 20 {
		t.Fatalf("expected at least 20 labeled queries, got %d", len(queries))
	}

	type row struct {
		query          string
		p5, rr, ndcg10 float64
	}
	rows := make([]row, 0, len(queries))
	var sumP5, sumRR, sumNDCG float64

	for _, lq := range queries {
		results, err := eng.SearchVector(ctx, query.VectorOptions{
			Text:  lq.Query,
			Scope: "workload",
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("SearchVector(%q): %v", lq.Query, err)
		}

		ranked := idsOf(results)
		rel := resolveJudgments(lq.Judgments, ids)

		p5 := PrecisionAtK(ranked, rel, 5)
		rr := ReciprocalRank(ranked, rel)
		ndcg10 := NDCGAtK(ranked, rel, 10)

		rows = append(rows, row{lq.Query, p5, rr, ndcg10})
		sumP5 += p5
		sumRR += rr
		sumNDCG += ndcg10
	}

	n := float64(len(queries))
	meanP5, meanMRR, meanNDCG := sumP5/n, sumRR/n, sumNDCG/n

	t.Logf("=== Quality Eval: %s (%d docs, %d labeled queries) ===", label, len(corpus.Docs), len(queries))
	t.Logf("%-62s %6s %6s %8s", "query", "P@5", "RR", "NDCG@10")
	for _, r := range rows {
		q := r.query
		if len(q) > 62 {
			q = q[:59] + "..."
		}
		t.Logf("%-62s %6.3f %6.3f %8.3f", q, r.p5, r.rr, r.ndcg10)
	}
	t.Logf("%-62s %6.3f %6.3f %8.3f", "MEAN", meanP5, meanMRR, meanNDCG)

	const saturationCeiling = 0.999
	if meanNDCG >= saturationCeiling {
		t.Fatalf("mean NDCG@10 = %.4f is saturated (>= %.3f); harden labels/distractors", meanNDCG, saturationCeiling)
	}
	if meanP5 <= 0 {
		t.Fatalf("mean P@5 = %.4f; workload/labels are broken", meanP5)
	}
}

// TestQualityEvalFake runs the quality eval hermetically against the
// deterministic FakeEmbedder. This always runs, including in CI.
func TestQualityEvalFake(t *testing.T) {
	runQualityEval(t, context.Background(), NewFakeEmbedder(fakeEmbedDim), "fake-hashing-v1 (hermetic)")
}

// TestQualityEvalReal runs the same quality eval against the real hugot
// embedder (sentence-transformers/all-MiniLM-L6-v2). It requires the model
// cache at ~/.known/models and is skipped unless KNOWN_BENCH_FULL=1:
//
//	KNOWN_BENCH_FULL=1 go test -tags bench ./bench/ -run TestQualityEval -v
func TestQualityEvalReal(t *testing.T) {
	if os.Getenv("KNOWN_BENCH_FULL") != "1" {
		t.Skip("set KNOWN_BENCH_FULL=1 to run the real-embedder quality eval")
	}
	cfg := embed.Config{
		Embedder:     "hugot",
		Model:        "sentence-transformers/all-MiniLM-L6-v2",
		CacheEnabled: true,
	}
	embedder, err := embed.NewEmbedder(cfg)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	runQualityEval(t, context.Background(), embedder, embedder.ModelName()+" (KNOWN_BENCH_FULL=1)")
}

// =============================================================================
// Discrimination self-test
// =============================================================================

// TestDiscrimination is the falsification check for the quality-eval
// pipeline: it takes the real ranked results for every labeled query and
// compares MRR/NDCG@10 against the *same result set reversed* -- a
// deliberately degraded ranking that keeps every relevant document in the
// candidate set but puts them in the worst plausible order. If the metrics
// pipeline can't tell a reversed ranking from the real one, it isn't
// measuring anything. Runs entirely against FakeEmbedder, so it is
// hermetic.
func TestDiscrimination(t *testing.T) {
	ctx := context.Background()
	fake := NewFakeEmbedder(fakeEmbedDim)
	eng, corpus, ids := buildScaleCorpus(t, ctx, 1000, fake)
	queries := BuildLabeledQueries(corpus)

	var realRRs, degradedRRs []float64
	var sumRealNDCG, sumDegradedNDCG float64
	n := 0

	// Fetch a wider candidate window than the quality eval's Limit:10 so
	// reversing it produces a genuinely bad top-10 (the tail of the
	// window) rather than just re-ordering an already-strong top-10 --
	// reversing only the top 10 barely moves the needle when more than
	// half of them are already relevant.
	const fetchLimit = 50
	for _, lq := range queries {
		results, err := eng.SearchVector(ctx, query.VectorOptions{
			Text:  lq.Query,
			Scope: "workload",
			Limit: fetchLimit,
		})
		if err != nil {
			t.Fatalf("SearchVector(%q): %v", lq.Query, err)
		}
		if len(results) == 0 {
			continue
		}

		full := idsOf(results)
		ranked := full
		if len(ranked) > 10 {
			ranked = ranked[:10]
		}
		degraded := reversed(full)
		rel := resolveJudgments(lq.Judgments, ids)

		realRRs = append(realRRs, ReciprocalRank(ranked, rel))
		degradedRRs = append(degradedRRs, ReciprocalRank(degraded, rel))
		sumRealNDCG += NDCGAtK(ranked, rel, 10)
		sumDegradedNDCG += NDCGAtK(degraded, rel, 10)
		n++
	}
	if n == 0 {
		t.Fatal("no queries returned results")
	}

	realMRR := MRR(realRRs)
	degradedMRR := MRR(degradedRRs)
	realNDCG := sumRealNDCG / float64(n)
	degradedNDCG := sumDegradedNDCG / float64(n)

	t.Logf("real:     MRR=%.4f NDCG@10=%.4f", realMRR, realNDCG)
	t.Logf("degraded: MRR=%.4f NDCG@10=%.4f (reversed ranking)", degradedMRR, degradedNDCG)

	const minGap = 0.05
	if realMRR-degradedMRR < minGap {
		t.Errorf("MRR did not discriminate: real=%.4f degraded=%.4f, want gap >= %.2f", realMRR, degradedMRR, minGap)
	}
	if realNDCG-degradedNDCG < minGap {
		t.Errorf("NDCG@10 did not discriminate: real=%.4f degraded=%.4f, want gap >= %.2f", realNDCG, degradedNDCG, minGap)
	}
}

// =============================================================================
// Labeled dataset drift check
// =============================================================================

// TestLabeledQueriesUpToDate regenerates labeled queries in-memory from the
// current workload.go definitions and fails if they no longer match the
// checked-in bench/testdata/labeled_queries.json. This is the guardrail for
// the "JSON is a generated artifact of the code, not a second source of
// truth" design: run
//
//	go run -tags bench bench/cmd/workloadgen/main.go
//
// and commit the result whenever workload.go's topics/facts/queries change.
func TestLabeledQueriesUpToDate(t *testing.T) {
	path := filepath.Join(benchDir(), "testdata", "labeled_queries.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run: go run -tags bench bench/cmd/workloadgen/main.go)", path, err)
	}
	var onDisk []LabeledQuery
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}

	fresh := BuildLabeledQueries(GenerateCorpus(1000))

	if !reflect.DeepEqual(onDisk, fresh) {
		t.Fatalf("bench/testdata/labeled_queries.json is stale relative to workload.go; "+
			"regenerate with: go run -tags bench bench/cmd/workloadgen/main.go "+
			"(on-disk queries=%d, fresh queries=%d)", len(onDisk), len(fresh))
	}
}
