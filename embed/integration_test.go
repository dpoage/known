//go:build integration

package embed

import (
	"context"
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
