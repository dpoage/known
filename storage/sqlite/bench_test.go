package sqlite

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// =============================================================================
// Vector Function Benchmarks
// =============================================================================

// BenchmarkCosineDistance measures raw cosine distance computation
// at the three common embedding dimensions: 384, 768, and 1536.
func BenchmarkCosineDistance(b *testing.B) {
	dims := []int{384, 768, 1536}
	for _, dim := range dims {
		dim := dim
		b.Run(fmt.Sprintf("dim=%d", dim), func(b *testing.B) {
			a := randomFloat32Slice(dim)
			v := randomFloat32Slice(dim)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = cosineDistance(a, v)
			}
		})
	}
}

// BenchmarkDeserializeFloat32 measures the cost of converting a raw BLOB
// back to a []float32 slice at the three common embedding dimensions.
func BenchmarkDeserializeFloat32(b *testing.B) {
	dims := []int{384, 768, 1536}
	for _, dim := range dims {
		dim := dim
		b.Run(fmt.Sprintf("dim=%d", dim), func(b *testing.B) {
			blob := randomFloat32Blob(dim)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = deserializeFloat32(blob)
			}
		})
	}
}

// =============================================================================
// End-to-End Search Benchmarks
// =============================================================================

// BenchmarkSearchSimilar benchmarks the full SearchSimilar path (two SQL passes
// + in-process ranking) at dataset sizes of 100, 1 000, and 10 000 entries
// all using 384-dimensional embeddings.
func BenchmarkSearchSimilar(b *testing.B) {
	const dim = 384
	sizes := []int{100, 1_000, 10_000}

	for _, n := range sizes {
		n := n
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := newBenchDB(b)
			entries := db.Entries()
			ctx := context.Background()

			seedEntries(b, ctx, entries, "bench", n, dim)

			query := randomFloat32Slice(dim)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results, err := entries.SearchSimilar(ctx, query, "bench", storage.Cosine, 10)
				if err != nil {
					b.Fatalf("SearchSimilar: %v", err)
				}
				_ = results
			}
		})
	}
}

// =============================================================================
// Helpers
// =============================================================================

// newBenchDB creates a fresh in-memory SQLite database, runs migrations, and
// registers cleanup via b.Cleanup so it is closed when the benchmark ends.
func newBenchDB(b *testing.B) *DB {
	b.Helper()
	ctx := context.Background()

	db, err := New(ctx, ":memory:")
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		b.Fatalf("Migrate: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

// seedEntries inserts n entries with random dim-dimensional embeddings into
// the given scope. Each entry gets a unique content string so the
// (content_hash, scope) unique constraint is never violated.
func seedEntries(b *testing.B, ctx context.Context, entries storage.EntryRepo, scope string, n, dim int) {
	b.Helper()
	src := model.Source{Type: model.SourceManual, Reference: "bench"}
	for i := 0; i < n; i++ {
		emb := randomFloat32Slice(dim)
		e := model.NewEntry(fmt.Sprintf("bench-entry-%d", i), src).
			WithScope(scope).
			WithEmbedding(emb, "bench-model")
		if err := entries.Create(ctx, &e); err != nil {
			b.Fatalf("seed entry %d: %v", i, err)
		}
	}
}

// randomFloat32Slice returns a slice of n pseudo-random float32 values in [0, 1).
func randomFloat32Slice(n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = rand.Float32()
	}
	return v
}

// randomFloat32Blob returns n float32 values encoded as a little-endian BLOB,
// mimicking the serialised form stored in the database.
func randomFloat32Blob(n int) []byte {
	buf := make([]byte, n*4)
	for i := 0; i < n; i++ {
		bits := math.Float32bits(rand.Float32())
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}
