//go:build integration

package embed

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"
)

// TestOllamaIntegration_Embed tests embedding with a real Ollama instance.
// Run with: go test -tags integration -run TestOllamaIntegration ./embed/...
//
// Prerequisites:
//   - Ollama running at localhost:11434
//   - Model pulled: ollama pull nomic-embed-text
func TestOllamaIntegration_Embed(t *testing.T) {
	ollamaURL := "http://localhost:11434"

	// Check if Ollama is reachable.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ollamaURL)
	if err != nil {
		t.Skipf("Ollama not available at %s: %v", ollamaURL, err)
	}
	resp.Body.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      ollamaURL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Single embedding.
	result, err := emb.Embed(ctx, "The Go programming language is statically typed and compiled.")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("expected non-empty embedding")
	}
	t.Logf("Embedding dimensions: %d", len(result))
	t.Logf("First 5 values: %v", result[:min(5, len(result))])

	if emb.Dimensions() != len(result) {
		t.Errorf("Dimensions() = %d, want %d", emb.Dimensions(), len(result))
	}

	// Batch embedding.
	texts := []string{
		"Go is a systems programming language.",
		"Python is an interpreted language.",
		"Rust emphasizes memory safety.",
	}
	batch, err := emb.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != len(texts) {
		t.Errorf("EmbedBatch returned %d results, want %d", len(batch), len(texts))
	}
	for i, vec := range batch {
		if len(vec) != emb.Dimensions() {
			t.Errorf("batch[%d] has %d dims, want %d", i, len(vec), emb.Dimensions())
		}
	}
}

// TestHugotIntegration_Embed tests the hugot pure Go embedder end-to-end.
// Run with: go test -tags integration -run TestHugotIntegration ./embed/...
//
// This downloads the all-MiniLM-L6-v2 model on first run (~80MB).
func TestHugotIntegration_Embed(t *testing.T) {
	cfg := Config{
		Embedder: "hugot",
		Model:    defaultHugotModel,
	}

	emb, err := NewHugotEmbedder(cfg)
	if err != nil {
		t.Fatalf("NewHugotEmbedder: %v", err)
	}
	defer emb.Destroy()

	ctx := context.Background()

	// Single embedding.
	result, err := emb.Embed(ctx, "The Go programming language is statically typed and compiled.")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(result) != 384 {
		t.Fatalf("expected 384 dimensions, got %d", len(result))
	}
	if emb.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d, want 384", emb.Dimensions())
	}
	if emb.ModelName() != defaultHugotModel {
		t.Errorf("ModelName() = %q, want %q", emb.ModelName(), defaultHugotModel)
	}

	// Batch embedding.
	texts := []string{
		"Go is a systems programming language.",
		"Python is an interpreted language.",
		"Rust emphasizes memory safety.",
	}
	batch, err := emb.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != len(texts) {
		t.Errorf("EmbedBatch returned %d results, want %d", len(batch), len(texts))
	}

	// Empty batch.
	empty, err := emb.EmbedBatch(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): %v", err)
	}
	if empty != nil {
		t.Errorf("EmbedBatch(nil) = %v, want nil", empty)
	}

	// Semantic similarity quality check.
	related := []string{
		"Go is a programming language",
		"Golang is a compiled language",
	}
	unrelated := "The weather is nice today"

	relOut, err := emb.EmbedBatch(ctx, related)
	if err != nil {
		t.Fatalf("EmbedBatch(related): %v", err)
	}
	unrelOut, err := emb.Embed(ctx, unrelated)
	if err != nil {
		t.Fatalf("Embed(unrelated): %v", err)
	}

	simRelated := cosineSimF32(relOut[0], relOut[1])
	simUnrelated := cosineSimF32(relOut[0], unrelOut)

	t.Logf("Similarity(Go/Golang): %.4f", simRelated)
	t.Logf("Similarity(Go/weather): %.4f", simUnrelated)

	if simRelated < 0.7 {
		t.Errorf("expected related similarity > 0.7, got %.4f", simRelated)
	}
	if simRelated <= simUnrelated {
		t.Errorf("expected related (%.4f) > unrelated (%.4f)", simRelated, simUnrelated)
	}
}

func cosineSimF32(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
