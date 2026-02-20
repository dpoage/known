package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// CachedEmbedder wraps any Embedder with an in-memory content-hash cache.
// Cache keys are SHA-256(model + "\x00" + text) so that different models
// produce distinct cache entries for the same text.
type CachedEmbedder struct {
	inner Embedder
	mu    sync.RWMutex
	cache map[string][]float32
}

// NewCachedEmbedder wraps an existing Embedder with caching.
func NewCachedEmbedder(inner Embedder) *CachedEmbedder {
	return &CachedEmbedder{
		inner: inner,
		cache: make(map[string][]float32),
	}
}

// Embed returns a cached embedding or delegates to the inner Embedder.
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := c.cacheKey(text)

	c.mu.RLock()
	if emb, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return emb, nil
	}
	c.mu.RUnlock()

	emb, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[key] = emb
	c.mu.Unlock()

	return emb, nil
}

// EmbedBatch returns cached embeddings where available and batches the
// remaining texts through the inner Embedder.
func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	results := make([][]float32, len(texts))
	var missingTexts []string
	var missingIndices []int

	c.mu.RLock()
	for i, text := range texts {
		key := c.cacheKey(text)
		if emb, ok := c.cache[key]; ok {
			results[i] = emb
		} else {
			missingTexts = append(missingTexts, text)
			missingIndices = append(missingIndices, i)
		}
	}
	c.mu.RUnlock()

	if len(missingTexts) == 0 {
		return results, nil
	}

	computed, err := c.inner.EmbedBatch(ctx, missingTexts)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	for j, idx := range missingIndices {
		results[idx] = computed[j]
		c.cache[c.cacheKey(missingTexts[j])] = computed[j]
	}
	c.mu.Unlock()

	return results, nil
}

// Dimensions delegates to the inner Embedder.
func (c *CachedEmbedder) Dimensions() int {
	return c.inner.Dimensions()
}

// ModelName delegates to the inner Embedder.
func (c *CachedEmbedder) ModelName() string {
	return c.inner.ModelName()
}

// Len returns the number of entries currently in the cache.
func (c *CachedEmbedder) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Clear removes all entries from the cache.
func (c *CachedEmbedder) Clear() {
	c.mu.Lock()
	c.cache = make(map[string][]float32)
	c.mu.Unlock()
}

// cacheKey produces a deterministic key from model name and text content.
func (c *CachedEmbedder) cacheKey(text string) string {
	h := sha256.New()
	h.Write([]byte(c.inner.ModelName()))
	h.Write([]byte{0}) // separator
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}
