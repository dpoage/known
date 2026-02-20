package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// OllamaEmbedder produces embeddings via the Ollama REST API.
//
// Ollama endpoint: POST <baseURL>/api/embed
//
//	Request:  {"model": "...", "input": "..." | ["...", ...]}
//	Response: {"model": "...", "embeddings": [[...]]}
type OllamaEmbedder struct {
	baseURL    string
	model      string
	client     *http.Client
	dimensions int
	mu         sync.RWMutex // guards dimensions
}

// NewOllamaEmbedder creates an Embedder that talks to a local Ollama instance.
func NewOllamaEmbedder(cfg Config) (*OllamaEmbedder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("ollama config: %w", err)
	}
	return &OllamaEmbedder{
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		model:      cfg.Model,
		client:     &http.Client{},
		dimensions: cfg.Dimensions,
	}, nil
}

// ollamaRequest is the JSON body sent to Ollama /api/embed.
type ollamaRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

// ollamaResponse is the JSON body returned by Ollama /api/embed.
type ollamaResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// Embed returns the embedding for a single text.
func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := o.doEmbed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama: empty embeddings response")
	}
	return results[0], nil
}

// EmbedBatch returns embeddings for multiple texts in a single API call.
func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return o.doEmbed(ctx, texts)
}

// Dimensions returns the vector dimensionality. If not configured up front,
// this is detected from the first successful embedding call and may return 0
// until then.
func (o *OllamaEmbedder) Dimensions() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.dimensions
}

// ModelName returns the Ollama model identifier.
func (o *OllamaEmbedder) ModelName() string {
	return o.model
}

// doEmbed performs the HTTP call. input can be string or []string.
func (o *OllamaEmbedder) doEmbed(ctx context.Context, input any) ([][]float32, error) {
	body, err := json.Marshal(ollamaRequest{
		Model: o.model,
		Input: input,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result ollamaResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: empty embeddings in response")
	}

	// Convert float64 -> float32 and auto-detect dimensions.
	out := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		f32 := make([]float32, len(emb))
		for j, v := range emb {
			f32[j] = float32(v)
		}
		out[i] = f32
	}

	// Auto-detect dimensions from first non-empty response.
	o.mu.Lock()
	if o.dimensions == 0 && len(out[0]) > 0 {
		o.dimensions = len(out[0])
	}
	o.mu.Unlock()

	return out, nil
}

// truncate shortens s to at most n characters for error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
