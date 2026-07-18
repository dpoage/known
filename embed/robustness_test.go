package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Timeout tests — context cancellation fires before slow server responds
// =============================================================================

func TestOllamaEmbedder_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "test-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err = emb.Embed(ctx, "hello"); err == nil {
		t.Fatal("expected error from context timeout, got nil")
	}
}

func TestOpenAIEmbedder_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "test-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err = emb.Embed(ctx, "hello"); err == nil {
		t.Fatal("expected error from context timeout, got nil")
	}
}

// =============================================================================
// Body size limit — server returns 51 MB; LimitReader truncates; JSON decode fails
// =============================================================================

func TestOllamaEmbedder_BodySizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 1024*1024) // 1 MB of garbage
		for range 51 {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "test-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	if _, err = emb.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected error from oversized response, got nil")
	}
}

func TestOpenAIEmbedder_BodySizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 1024*1024) // 1 MB of garbage
		for range 51 {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "test-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	if _, err = emb.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected error from oversized response, got nil")
	}
}

// =============================================================================
// Batch chunking — 300 texts must arrive as 2 HTTP calls: 256 + 44
// =============================================================================

func TestOpenAIEmbedder_BatchChunking(t *testing.T) {
	var callCount atomic.Int32
	batchSizes := make([]int, 0, 2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(callCount.Add(1))

		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("call %d: decode request: %v", call, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		n := len(req.Input)
		batchSizes = append(batchSizes, n)

		data := make([]openaiEmbedding, n)
		for i := range n {
			data[i] = openaiEmbedding{
				Index:     i,
				Embedding: []float64{float64(i), 0, 0},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openaiResponse{Data: data})
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "test-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	texts := make([]string, 300)
	for i := range texts {
		texts[i] = "text"
	}

	results, err := emb.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	if got := int(callCount.Load()); got != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", got)
	}
	if len(batchSizes) >= 1 && batchSizes[0] != 256 {
		t.Errorf("first batch: expected 256, got %d", batchSizes[0])
	}
	if len(batchSizes) >= 2 && batchSizes[1] != 44 {
		t.Errorf("second batch: expected 44, got %d", batchSizes[1])
	}
	if len(results) != 300 {
		t.Errorf("expected 300 results, got %d", len(results))
	}
}

// =============================================================================
// HugotEmbedder.Close — nil session must not panic
// =============================================================================

func TestHugotEmbedder_CloseNilSession(t *testing.T) {
	h := &HugotEmbedder{}
	if err := h.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
