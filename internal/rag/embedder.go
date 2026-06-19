package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	embeddingModel = "text-embedding-3-small"
	// EmbeddingDim is the output dimension of text-embedding-3-small.
	// We chose this model because it costs $0.02/million tokens vs $0.10 for
	// ada-002 with equal or better benchmark quality.
	// 1536 dims = 6KB per vector in pgvector (1536 × 4 bytes float32).
	// Interview talking point: "I chose text-embedding-3-small because at
	// 100K notifications/day that's 100K embeddings/day — at ada-002 pricing
	// that's $10/day just for indexing vs $2/day with 3-small."
	EmbeddingDim = 1536
)

// Embedder converts text into dense vectors using OpenAI's embedding API.
// The vector captures semantic meaning — two texts with similar meaning have
// vectors that are close together in cosine space even if they use completely
// different words. This is what makes RAG work: "delivery failed" ≈ "not received".
type Embedder struct {
	apiKey     string
	httpClient *http.Client
}

// NewEmbedder creates a new embedder with the given OpenAI API key.
func NewEmbedder(apiKey string) *Embedder {
	return &Embedder{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type embedRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage *struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Embed converts a string into a 1536-dimensional float32 vector.
// The vector is used for semantic similarity search in pgvector.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("cannot embed empty text")
	}

	body, err := json.Marshal(embedRequest{Input: text, Model: embeddingModel})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result embedResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("OpenAI embedding error (%s): %s", result.Error.Type, result.Error.Message)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	if len(result.Data[0].Embedding) != EmbeddingDim {
		return nil, fmt.Errorf("unexpected embedding dimension: got %d, want %d",
			len(result.Data[0].Embedding), EmbeddingDim)
	}
	return result.Data[0].Embedding, nil
}
