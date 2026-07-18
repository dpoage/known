package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// TestInnerProductDistance verifies the innerProductDistance function.
func TestInnerProductDistance(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	got := innerProductDistance(a, b)
	want := -1.0
	if got != want {
		t.Errorf("innerProductDistance([1,0,0],[1,0,0]) = %f, want %f", got, want)
	}

	// Orthogonal vectors: dot = 0, so distance = 0.
	c := []float32{0, 1, 0}
	got2 := innerProductDistance(a, c)
	if got2 != 0.0 {
		t.Errorf("innerProductDistance([1,0,0],[0,1,0]) = %f, want 0.0", got2)
	}
}

// TestParseTimeInvalid verifies parseTime returns zero time for bad input (no panic).
func TestParseTimeInvalid(t *testing.T) {
	result := parseTime("not-a-time")
	if !result.IsZero() {
		t.Errorf("parseTime(invalid) = %v, want zero time", result)
	}
}

// TestLoadLabelsForEntriesLargeBatch verifies batching works for >500 entries.
func TestLoadLabelsForEntriesLargeBatch(t *testing.T) {
	ctx := context.Background()
	db, err := New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	entries := db.Entries()
	scopes := db.Scopes()

	sc := model.NewScope("lblbatch")
	if err := scopes.Upsert(ctx, &sc); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	const n = 600
	created := make([]model.Entry, n)
	for i := range created {
		e := model.NewEntry(fmt.Sprintf("entry-%d", i), src).WithScope("lblbatch").
			WithLabels([]string{"tag"})
		if err := entries.Create(ctx, &e); err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
		created[i] = e
	}
	// Clear in-memory labels so loadLabelsForEntries starts from scratch (simulates fresh query).
	for i := range created {
		created[i].Labels = nil
	}

	// loadLabelsForEntries directly on 600 entries should not error.
	if err := loadLabelsForEntries(ctx, db.db, created); err != nil {
		t.Fatalf("loadLabelsForEntries(600): %v", err)
	}
	// All entries should have their label.
	for i, e := range created {
		if len(e.Labels) != 1 || e.Labels[0] != "tag" {
			t.Errorf("entry[%d]: labels = %v, want [tag]", i, e.Labels)
			break
		}
	}
}

// TestSearchSimilarInnerProduct verifies InnerProduct metric works end-to-end.
func TestSearchSimilarInnerProduct(t *testing.T) {
	ctx := context.Background()
	db, err := New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	entries := db.Entries()
	scopes := db.Scopes()

	sc := model.NewScope("iptest")
	if err := scopes.Upsert(ctx, &sc); err != nil {
		t.Fatalf("Upsert scope: %v", err)
	}

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e := model.NewEntry("vec entry", src).WithScope("iptest").
		WithEmbedding([]float32{1.0, 0.0, 0.0}, "test-model")
	if err := entries.Create(ctx, &e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, err := entries.SearchSimilar(ctx, []float32{1.0, 0.0, 0.0}, "iptest", storage.InnerProduct, 5)
	if err != nil {
		t.Fatalf("SearchSimilar(InnerProduct): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchSimilar(InnerProduct): no results")
	}
	// Negative inner product of identical unit vectors = -1.0; distance must be negative.
	if results[0].Distance >= 0 {
		t.Errorf("InnerProduct distance for identical vectors = %f, want < 0", results[0].Distance)
	}
}
