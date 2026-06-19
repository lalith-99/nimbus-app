package rag

import (
	"context"
	"math"
	"sort"
	"strings"
)

// Reranker applies a second-pass scoring to a list of retrieved documents.
//
// Two-stage retrieval pattern (industry standard):
//
//	Stage 1 — Recall (this is what Store.HybridSearch does):
//	  Cast a wide net — retrieve top-20 candidates cheaply using
//	  approximate vector search (HNSW index) + full-text.
//	  Goal: high recall. It's OK to include some irrelevant docs here.
//
//	Stage 2 — Precision (this is what Reranker does):
//	  Score each candidate more carefully against the exact query.
//	  Reorder so the most relevant docs are at the top.
//	  Goal: high precision for the final top-5 passed to the LLM.
//
// Why not just use the vector scores from stage 1?
// Vector scores capture semantic similarity but don't account for query
// term frequency or positional importance. A cross-encoder (even a simple
// one) sees the query AND document together, which gives better precision.
//
// Production: use a dedicated reranker model (Cohere Rerank, BGE-Reranker-v2).
// Those are cross-encoders fine-tuned specifically for ranking.
// Here we use a BM25-inspired approximation — same conceptual shape,
// no extra API call.
type Reranker struct{}

// NewReranker creates a new reranker.
func NewReranker() *Reranker { return &Reranker{} }

// Rerank scores each document against the query and returns the top-k.
// The documents are sorted in-place and a slice of length topK is returned.
func (r *Reranker) Rerank(_ context.Context, query string, docs []*Document, topK int) []*Document {
	if len(docs) == 0 {
		return docs
	}

	queryTerms := tokenize(query)
	for _, doc := range docs {
		doc.Score = crossEncoderScore(queryTerms, doc.Content)
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Score > docs[j].Score
	})

	if topK > len(docs) {
		topK = len(docs)
	}
	return docs[:topK]
}

// crossEncoderScore computes a BM25-inspired relevance score.
//
// BM25 components used here:
//
//	TF(t,d) = freq(t,d) / len(d)   — term frequency in document
//	Score   = Σ log(1 + TF(t,d))   for each query term t found in document d
//
// Why log? It dampens the effect of very frequent terms so one term
// appearing 100 times isn't 100× more important than appearing once.
func crossEncoderScore(queryTerms []string, docContent string) float32 {
	docTerms := tokenize(docContent)
	if len(docTerms) == 0 {
		return 0
	}

	freq := make(map[string]int, len(docTerms))
	for _, t := range docTerms {
		freq[t]++
	}

	var score float64
	for _, qt := range queryTerms {
		if f, ok := freq[qt]; ok {
			tf := float64(f) / float64(len(docTerms))
			score += math.Log(1 + tf)
		}
	}
	return float32(score)
}

// tokenize lowercases, strips punctuation, and removes stopwords.
// Reused by both the reranker and the guard's PII check.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	text = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, text)

	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "have": true,
		"has": true, "had": true, "do": true, "does": true, "did": true,
		"for": true, "of": true, "to": true, "in": true, "on": true,
		"at": true, "by": true, "with": true, "from": true, "that": true,
		"this": true, "it": true, "as": true, "or": true, "and": true,
	}

	var result []string
	for _, p := range strings.Fields(text) {
		if !stopwords[p] && len(p) > 1 {
			result = append(result, p)
		}
	}
	return result
}
