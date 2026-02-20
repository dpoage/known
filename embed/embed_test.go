package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// =============================================================================
// Ollama Embedder Tests
// =============================================================================

func TestOllamaEmbedder_Embed(t *testing.T) {
	want := []float64{0.1, 0.2, 0.3, 0.4}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected /api/embed, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}

		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("expected model nomic-embed-text, got %s", req.Model)
		}

		resp := ollamaResponse{
			Model:      "nomic-embed-text",
			Embeddings: [][]float64{want},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	result, err := emb.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(result) != len(want) {
		t.Fatalf("expected %d dimensions, got %d", len(want), len(result))
	}
	for i, v := range want {
		if math.Abs(float64(result[i])-v) > 1e-6 {
			t.Errorf("result[%d] = %f, want %f", i, result[i], v)
		}
	}

	// Dimensions should be auto-detected.
	if emb.Dimensions() != len(want) {
		t.Errorf("Dimensions() = %d, want %d", emb.Dimensions(), len(want))
	}
	if emb.ModelName() != "nomic-embed-text" {
		t.Errorf("ModelName() = %q, want %q", emb.ModelName(), "nomic-embed-text")
	}
}

func TestOllamaEmbedder_EmbedBatch(t *testing.T) {
	embeddings := [][]float64{
		{0.1, 0.2},
		{0.3, 0.4},
		{0.5, 0.6},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := ollamaResponse{
			Model:      "nomic-embed-text",
			Embeddings: embeddings,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	results, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, emb := range results {
		if len(emb) != 2 {
			t.Errorf("result[%d] has %d dims, want 2", i, len(emb))
		}
	}
}

func TestOllamaEmbedder_EmbedBatch_Empty(t *testing.T) {
	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      "http://localhost:99999", // should never be called
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	results, err := emb.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): %v", err)
	}
	if results != nil {
		t.Errorf("EmbedBatch(nil) = %v, want nil", results)
	}
}

func TestOllamaEmbedder_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "model not found"}`))
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nonexistent-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestOllamaEmbedder_ConnectionRefused(t *testing.T) {
	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      "http://127.0.0.1:1", // nothing listening
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestOllamaEmbedder_InvalidConfig(t *testing.T) {
	_, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "",
		URL:      "http://localhost:11434",
	})
	if err == nil {
		t.Fatal("expected validation error for empty model")
	}
}

func TestOllamaEmbedder_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is cancelled — the client should give up.
		<-r.Context().Done()
	}))
	defer srv.Close()

	emb, err := NewOllamaEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = emb.Embed(ctx, "hello")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestOllamaEmbedder_ConfiguredDimensions(t *testing.T) {
	emb, err := NewOllamaEmbedder(Config{
		Embedder:   "ollama",
		Model:      "nomic-embed-text",
		URL:        "http://localhost:11434",
		Dimensions: 768,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}

	if emb.Dimensions() != 768 {
		t.Errorf("Dimensions() = %d, want 768", emb.Dimensions())
	}
}

// =============================================================================
// OpenAI-Compatible Embedder Tests
// =============================================================================

func TestOpenAIEmbedder_Embed(t *testing.T) {
	want := []float64{0.5, 0.6, 0.7}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected /v1/embeddings, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", auth)
		}

		var req openaiRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "text-embedding-3-small" {
			t.Errorf("expected model text-embedding-3-small, got %s", req.Model)
		}
		if len(req.Input) != 1 {
			t.Errorf("expected 1 input, got %d", len(req.Input))
		}

		resp := openaiResponse{
			Model: "text-embedding-3-small",
			Data: []openaiEmbedding{
				{Embedding: want, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "text-embedding-3-small",
		URL:      srv.URL,
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	result, err := emb.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(result) != len(want) {
		t.Fatalf("expected %d dimensions, got %d", len(want), len(result))
	}
	for i, v := range want {
		if math.Abs(float64(result[i])-v) > 1e-6 {
			t.Errorf("result[%d] = %f, want %f", i, result[i], v)
		}
	}

	if emb.Dimensions() != len(want) {
		t.Errorf("Dimensions() = %d, want %d", emb.Dimensions(), len(want))
	}
	if emb.ModelName() != "text-embedding-3-small" {
		t.Errorf("ModelName() = %q, want %q", emb.ModelName(), "text-embedding-3-small")
	}
}

func TestOpenAIEmbedder_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		json.NewDecoder(r.Body).Decode(&req)

		data := make([]openaiEmbedding, len(req.Input))
		for i := range req.Input {
			data[i] = openaiEmbedding{
				Embedding: []float64{float64(i) * 0.1, float64(i) * 0.2},
				Index:     i,
			}
		}

		resp := openaiResponse{
			Model: "text-embedding-3-small",
			Data:  data,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "text-embedding-3-small",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	results, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestOpenAIEmbedder_EmbedBatch_UnorderedResponse(t *testing.T) {
	// The OpenAI API does not guarantee ordering by index.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return indices in reverse order.
		resp := openaiResponse{
			Model: "model",
			Data: []openaiEmbedding{
				{Embedding: []float64{0.9}, Index: 2},
				{Embedding: []float64{0.1}, Index: 0},
				{Embedding: []float64{0.5}, Index: 1},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	results, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	// Verify order matches input, not response order.
	if math.Abs(float64(results[0][0])-0.1) > 1e-6 {
		t.Errorf("results[0][0] = %f, want 0.1", results[0][0])
	}
	if math.Abs(float64(results[1][0])-0.5) > 1e-6 {
		t.Errorf("results[1][0] = %f, want 0.5", results[1][0])
	}
	if math.Abs(float64(results[2][0])-0.9) > 1e-6 {
		t.Errorf("results[2][0] = %f, want 0.9", results[2][0])
	}
}

func TestOpenAIEmbedder_EmbedBatch_Empty(t *testing.T) {
	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "model",
		URL:      "http://localhost:99999",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	results, err := emb.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): %v", err)
	}
	if results != nil {
		t.Errorf("EmbedBatch(nil) = %v, want nil", results)
	}
}

func TestOpenAIEmbedder_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Error: &openaiError{
				Message: "invalid model",
				Type:    "invalid_request_error",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "bad-model",
		URL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestOpenAIEmbedder_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "model",
		URL:      srv.URL,
		APIKey:   "bad-key",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestOpenAIEmbedder_NoAuthHeader_WithoutAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := openaiResponse{
			Model: "model",
			Data: []openaiEmbedding{
				{Embedding: []float64{0.1}, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewOpenAICompatibleEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "model",
		URL:      srv.URL,
		// No APIKey set.
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEmbedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if gotAuth != "" {
		t.Errorf("Authorization header should be empty when no API key is set, got %q", gotAuth)
	}
}

// =============================================================================
// Cache Tests
// =============================================================================

func TestCachedEmbedder_Embed(t *testing.T) {
	var callCount atomic.Int32
	inner := &fakeEmbedder{
		dims:  3,
		model: "test-model",
		embedFn: func(text string) []float32 {
			callCount.Add(1)
			return []float32{0.1, 0.2, 0.3}
		},
	}

	cached := NewCachedEmbedder(inner)

	// First call should hit the inner embedder.
	result1, err := cached.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
	if len(result1) != 3 {
		t.Errorf("expected 3 dims, got %d", len(result1))
	}

	// Second call with same text should be cached.
	result2, err := cached.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call (cached), got %d", callCount.Load())
	}
	if len(result2) != 3 {
		t.Errorf("expected 3 dims, got %d", len(result2))
	}

	// Different text should hit the inner embedder.
	_, err = cached.Embed(context.Background(), "world")
	if err != nil {
		t.Fatalf("third Embed: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", callCount.Load())
	}

	if cached.Len() != 2 {
		t.Errorf("cache Len() = %d, want 2", cached.Len())
	}
}

func TestCachedEmbedder_EmbedBatch(t *testing.T) {
	var callCount atomic.Int32
	inner := &fakeEmbedder{
		dims:  2,
		model: "test-model",
		embedBatchFn: func(texts []string) [][]float32 {
			callCount.Add(1)
			out := make([][]float32, len(texts))
			for i := range texts {
				out[i] = []float32{float32(i), float32(i + 1)}
			}
			return out
		},
	}

	cached := NewCachedEmbedder(inner)

	// First batch call.
	results1, err := cached.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("first EmbedBatch: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
	if len(results1) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results1))
	}

	// Second batch with partial overlap: "a" cached, "d" new.
	results2, err := cached.EmbedBatch(context.Background(), []string{"a", "d"})
	if err != nil {
		t.Fatalf("second EmbedBatch: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", callCount.Load())
	}
	if len(results2) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results2))
	}

	// Fully cached batch.
	_, err = cached.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("third EmbedBatch: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls (fully cached), got %d", callCount.Load())
	}
}

func TestCachedEmbedder_EmbedBatch_Empty(t *testing.T) {
	inner := &fakeEmbedder{dims: 2, model: "test-model"}
	cached := NewCachedEmbedder(inner)

	results, err := cached.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): %v", err)
	}
	if results != nil {
		t.Errorf("EmbedBatch(nil) = %v, want nil", results)
	}
}

func TestCachedEmbedder_Clear(t *testing.T) {
	inner := &fakeEmbedder{
		dims:  2,
		model: "test-model",
		embedFn: func(text string) []float32 {
			return []float32{0.1, 0.2}
		},
	}

	cached := NewCachedEmbedder(inner)
	cached.Embed(context.Background(), "hello")

	if cached.Len() != 1 {
		t.Fatalf("cache Len() = %d, want 1", cached.Len())
	}

	cached.Clear()
	if cached.Len() != 0 {
		t.Errorf("cache Len() after Clear() = %d, want 0", cached.Len())
	}
}

func TestCachedEmbedder_DelegatesMethods(t *testing.T) {
	inner := &fakeEmbedder{dims: 384, model: "nomic-embed-text"}
	cached := NewCachedEmbedder(inner)

	if cached.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d, want 384", cached.Dimensions())
	}
	if cached.ModelName() != "nomic-embed-text" {
		t.Errorf("ModelName() = %q, want %q", cached.ModelName(), "nomic-embed-text")
	}
}

// =============================================================================
// Factory Tests
// =============================================================================

func TestNewEmbedder_Ollama(t *testing.T) {
	emb, err := NewEmbedder(Config{
		Embedder: "ollama",
		Model:    "nomic-embed-text",
		URL:      "http://localhost:11434",
	})
	if err != nil {
		t.Fatalf("NewEmbedder(ollama): %v", err)
	}
	if emb.ModelName() != "nomic-embed-text" {
		t.Errorf("ModelName() = %q, want %q", emb.ModelName(), "nomic-embed-text")
	}
}

func TestNewEmbedder_OpenAICompatible(t *testing.T) {
	emb, err := NewEmbedder(Config{
		Embedder: "openai-compatible",
		Model:    "text-embedding-3-small",
		URL:      "https://api.openai.com",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("NewEmbedder(openai-compatible): %v", err)
	}
	if emb.ModelName() != "text-embedding-3-small" {
		t.Errorf("ModelName() = %q, want %q", emb.ModelName(), "text-embedding-3-small")
	}
}

func TestNewEmbedder_Unknown(t *testing.T) {
	_, err := NewEmbedder(Config{
		Embedder: "unknown",
		Model:    "model",
		URL:      "http://localhost",
	})
	if err == nil {
		t.Fatal("expected error for unknown embedder type")
	}
}

// =============================================================================
// Config Tests
// =============================================================================

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid ollama",
			cfg:     Config{Embedder: "ollama", Model: "nomic-embed-text", URL: "http://localhost:11434"},
			wantErr: false,
		},
		{
			name:    "valid openai-compatible",
			cfg:     Config{Embedder: "openai-compatible", Model: "text-embedding-3-small", URL: "https://api.openai.com"},
			wantErr: false,
		},
		{
			name:    "unknown embedder",
			cfg:     Config{Embedder: "foo", Model: "m", URL: "http://localhost"},
			wantErr: true,
		},
		{
			name:    "empty model",
			cfg:     Config{Embedder: "ollama", Model: "", URL: "http://localhost"},
			wantErr: true,
		},
		{
			name:    "empty URL",
			cfg:     Config{Embedder: "ollama", Model: "m", URL: ""},
			wantErr: true,
		},
		{
			name:    "negative dimensions",
			cfg:     Config{Embedder: "ollama", Model: "m", URL: "http://localhost", Dimensions: -1},
			wantErr: true,
		},
		{
			name:    "explicit zero dimensions OK",
			cfg:     Config{Embedder: "ollama", Model: "m", URL: "http://localhost", Dimensions: 0},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// fakeEmbedder is a test double that records calls and returns configured results.
type fakeEmbedder struct {
	dims         int
	model        string
	embedFn      func(text string) []float32
	embedBatchFn func(texts []string) [][]float32
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if f.embedFn != nil {
		return f.embedFn(text), nil
	}
	return make([]float32, f.dims), nil
}

func (f *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if f.embedBatchFn != nil {
		return f.embedBatchFn(texts), nil
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, f.dims)
	}
	return out, nil
}

func (f *fakeEmbedder) Dimensions() int  { return f.dims }
func (f *fakeEmbedder) ModelName() string { return f.model }
