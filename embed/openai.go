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

// OpenAICompatibleEmbedder produces embeddings via any OpenAI-compatible
// /v1/embeddings endpoint (OpenAI, Azure OpenAI, vLLM, LiteLLM, etc.).
//
// Endpoint: POST <baseURL>/v1/embeddings
//
//	Request:  {"model": "...", "input": ["...", ...]}
//	Response: {"data": [{"embedding": [...], "index": 0}], "model": "..."}
type OpenAICompatibleEmbedder struct {
	baseURL    string
	model      string
	apiKey     string
	client     *http.Client
	dimensions int
	mu         sync.RWMutex // guards dimensions
}

// NewOpenAICompatibleEmbedder creates an Embedder backed by an
// OpenAI-compatible embedding API.
func NewOpenAICompatibleEmbedder(cfg Config) (*OpenAICompatibleEmbedder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("openai-compatible config: %w", err)
	}
	return &OpenAICompatibleEmbedder{
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		model:      cfg.Model,
		apiKey:     cfg.APIKey,
		client:     &http.Client{},
		dimensions: cfg.Dimensions,
	}, nil
}

// openaiRequest is the JSON body sent to the embeddings endpoint.
type openaiRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// openaiResponse is the JSON body returned by the embeddings endpoint.
type openaiResponse struct {
	Data  []openaiEmbedding `json:"data"`
	Model string            `json:"model"`
	Error *openaiError      `json:"error,omitempty"`
}

type openaiEmbedding struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Embed returns the embedding for a single text.
func (o *OpenAICompatibleEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := o.doEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("openai-compatible: empty embeddings response")
	}
	return results[0], nil
}

// EmbedBatch returns embeddings for multiple texts in a single API call.
func (o *OpenAICompatibleEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return o.doEmbed(ctx, texts)
}

// Dimensions returns the vector dimensionality. Like OllamaEmbedder, this
// may be auto-detected from the first response.
func (o *OpenAICompatibleEmbedder) Dimensions() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.dimensions
}

// ModelName returns the configured model identifier.
func (o *OpenAICompatibleEmbedder) ModelName() string {
	return o.model
}

// doEmbed performs the HTTP call to the /v1/embeddings endpoint.
func (o *OpenAICompatibleEmbedder) doEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(openaiRequest{
		Model: o.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai-compatible: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai-compatible: decode response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("openai-compatible: API error: %s", result.Error.Message)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai-compatible: empty data in response")
	}

	// The response may not be sorted by index. Build by index.
	out := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("openai-compatible: unexpected index %d for batch size %d", d.Index, len(texts))
		}
		f32 := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			f32[j] = float32(v)
		}
		out[d.Index] = f32
	}

	// Verify every slot was populated.
	for i, emb := range out {
		if emb == nil {
			return nil, fmt.Errorf("openai-compatible: missing embedding at index %d", i)
		}
	}

	// Auto-detect dimensions.
	o.mu.Lock()
	if o.dimensions == 0 && len(out[0]) > 0 {
		o.dimensions = len(out[0])
	}
	o.mu.Unlock()

	return out, nil
}
