//go:build ignore

// workloadgen deterministically generates the synthetic benchmark corpus
// defined in bench/workload.go and derives graded relevance labels for it,
// writing them to bench/testdata/labeled_queries.json.
//
// Usage:
//
//	go run -tags bench bench/cmd/workloadgen/main.go [-n 1000] [-out path]
//
// Unlike cmd/seedgen, this does NOT persist a SQLite database: the corpus
// itself is regenerated in-process by bench/scale_test.go (via
// bench.GenerateCorpus), deterministically, every time the tests run --
// hermetically against bench.FakeEmbedder by default, or against the real
// embedder under KNOWN_BENCH_FULL=1. That guarantees the corpus the tests
// build always matches the corpus this tool derived labels from.
//
// -tags bench is required (not just "go run bench/cmd/workloadgen/main.go")
// because, unlike seedgen, this tool imports the bench package itself,
// whose files are gated by //go:build bench.
//
// Run this only when workload.go's topic/query/fact definitions change,
// then commit the regenerated JSON. bench.TestLabeledQueriesUpToDate fails
// the build if the checked-in JSON drifts from the generator.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dpoage/known/bench"
)

func main() {
	n := flag.Int("n", 1000, "corpus size used to derive labeled query judgments (must be >= 1000)")
	out := flag.String("out", "", "output path for labeled_queries.json (default: bench/testdata/labeled_queries.json)")
	flag.Parse()

	if *n < 1000 {
		log.Fatalf("-n must be >= 1000 (labeled queries reference the first 1000 generated docs)")
	}

	outPath := *out
	if outPath == "" {
		_, thisFile, _, _ := runtime.Caller(0)
		repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
		outPath = filepath.Join(repoRoot, "bench", "testdata", "labeled_queries.json")
	}

	corpus := bench.GenerateCorpus(*n)
	queries := bench.BuildLabeledQueries(corpus)

	fmt.Printf("Generated corpus: %d docs, %d edges, %d scopes\n", len(corpus.Docs), len(corpus.Edges), len(corpus.Scopes))
	fmt.Printf("Derived %d labeled queries\n", len(queries))
	totalJudgments := 0
	for _, q := range queries {
		totalJudgments += len(q.Judgments)
	}
	fmt.Printf("Total judgments across all queries: %d\n", totalJudgments)

	data, err := json.MarshalIndent(queries, "", "  ")
	if err != nil {
		log.Fatalf("marshal labeled queries: %v", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", outPath, err)
	}
	fmt.Printf("Wrote %s (%d bytes)\n", outPath, len(data))
}
