package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

// Default model for the hugot embedder.
const (
	defaultHugotModel    = "sentence-transformers/all-MiniLM-L6-v2"
	defaultHugotOnnxFile = "onnx/model.onnx"
	hugotModelSubdir     = "models"
)

// HugotEmbedder produces embeddings using a local ONNX model via hugot's
// pure Go backend (GoMLX). No external services or CGo required.
type HugotEmbedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	model    string
	dims     int
	mu       sync.RWMutex // guards dims
}

// NewHugotEmbedder creates an embedder that runs a sentence-transformer model
// locally using hugot's pure Go inference backend.
//
// On first use the model is downloaded from Hugging Face to ~/.known/models/.
func NewHugotEmbedder(cfg Config) (*HugotEmbedder, error) {
	model := cfg.Model
	if model == "" || model == "nomic-embed-text" {
		// Override the Ollama default model with the hugot default.
		model = defaultHugotModel
	}

	modelDir, err := hugotModelDir()
	if err != nil {
		return nil, fmt.Errorf("hugot: model directory: %w", err)
	}

	session, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("hugot: create session: %w", err)
	}

	downloadOpts := hugot.NewDownloadOptions()
	downloadOpts.OnnxFilePath = defaultHugotOnnxFile
	modelPath, err := hugot.DownloadModel(model, modelDir, downloadOpts)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("hugot: download model %q: %w", model, err)
	}

	pipelineCfg := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "known-embed",
	}
	pipeline, err := hugot.NewPipeline(session, pipelineCfg)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("hugot: create pipeline: %w", err)
	}

	e := &HugotEmbedder{
		session:  session,
		pipeline: pipeline,
		model:    model,
		dims:     cfg.Dimensions,
	}

	return e, nil
}

// Embed returns the embedding vector for a single text string.
func (h *HugotEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	output, err := h.pipeline.RunPipeline([]string{text})
	if err != nil {
		return nil, fmt.Errorf("hugot: embed: %w", err)
	}
	if len(output.Embeddings) == 0 {
		return nil, fmt.Errorf("hugot: empty embeddings response")
	}

	vec := output.Embeddings[0]
	h.detectDims(vec)
	return vec, nil
}

// EmbedBatch returns embedding vectors for multiple texts.
func (h *HugotEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	output, err := h.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("hugot: embed batch: %w", err)
	}
	if len(output.Embeddings) != len(texts) {
		return nil, fmt.Errorf("hugot: expected %d embeddings, got %d", len(texts), len(output.Embeddings))
	}

	if len(output.Embeddings) > 0 {
		h.detectDims(output.Embeddings[0])
	}
	return output.Embeddings, nil
}

// Dimensions returns the vector dimensionality. May return 0 until the first
// embedding call if not configured up front.
func (h *HugotEmbedder) Dimensions() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.dims
}

// ModelName returns the Hugging Face model identifier.
func (h *HugotEmbedder) ModelName() string {
	return h.model
}

// Destroy releases the hugot session resources.
func (h *HugotEmbedder) Destroy() {
	if h.session != nil {
		h.session.Destroy()
	}
}

// detectDims auto-detects dimensions from the first non-empty embedding.
func (h *HugotEmbedder) detectDims(vec []float32) {
	h.mu.Lock()
	if h.dims == 0 && len(vec) > 0 {
		h.dims = len(vec)
	}
	h.mu.Unlock()
}

// hugotModelDir returns the directory for storing downloaded models.
func hugotModelDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".known", hugotModelSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
