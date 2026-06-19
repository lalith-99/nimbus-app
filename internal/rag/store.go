package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Document is a retrieved chunk of text from the knowledge base.
type Document struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Content    string
	SourceType string // "notification" | "faq" | "doc"
	SourceID   *uuid.UUID
	Score      float32 // relevance score (higher = more relevant)
}

// Store handles vector storage and hybrid retrieval from pgvector.
type Store struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

// NewStore creates a new vector store backed by Postgres + pgvector.
func NewStore(db *pgxpool.Pool, logger *zap.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// Upsert stores a document with its embedding vector.
// We use INSERT ... ON CONFLICT DO UPDATE so re-indexing is idempotent —
// safe to call multiple times for the same document ID.
func (s *Store) Upsert(ctx context.Context, doc *Document, embedding []float32) error {
	vec := float32SliceToPgVector(embedding)
	_, err := s.db.Exec(ctx, `
		INSERT INTO knowledge_base (id, tenant_id, content, source_type, source_id, embedding)
		VALUES ($1, $2, $3, $4, $5, $6::vector)
		ON CONFLICT (id) DO UPDATE SET
			content     = EXCLUDED.content,
			embedding   = EXCLUDED.embedding,
			source_type = EXCLUDED.source_type,
			source_id   = EXCLUDED.source_id
	`, doc.ID, doc.TenantID, doc.Content, doc.SourceType, doc.SourceID, vec)
	if err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}
	s.logger.Debug("upserted document", zap.String("id", doc.ID.String()))
	return nil
}

// HybridSearch combines vector similarity and full-text search using
// Reciprocal Rank Fusion (RRF).
//
// Why hybrid over pure vector search?
//   - Pure vector search: great for semantic similarity ("delivery failed" ≈ "not received")
//     but misses exact matches (specific IDs, email addresses, error codes).
//   - Pure full-text (BM25): great for exact keywords but misses paraphrasing.
//   - Hybrid with RRF: best of both. No need for a common score scale between
//     the two systems — RRF just uses rank positions, not raw scores.
//
// RRF formula: score(d) = Σ 1 / (k + rank_i(d))   where k=60 is standard.
// A document ranked #1 in both lists gets: 1/(60+1) + 1/(60+1) ≈ 0.032.
// A document ranked #1 in only one list gets: 1/(60+1) ≈ 0.016.
func (s *Store) HybridSearch(
	ctx context.Context,
	tenantID uuid.UUID,
	queryEmbedding []float32,
	queryText string,
	limit int,
) ([]*Document, error) {
	vec := float32SliceToPgVector(queryEmbedding)

	rows, err := s.db.Query(ctx, `
		WITH semantic_search AS (
			SELECT id, content, source_type, source_id,
			       ROW_NUMBER() OVER (ORDER BY embedding <=> $1::vector) AS rank
			FROM knowledge_base
			WHERE tenant_id = $2
			ORDER BY embedding <=> $1::vector
			LIMIT 20
		),
		fulltext_search AS (
			SELECT id, content, source_type, source_id,
			       ROW_NUMBER() OVER (
			           ORDER BY ts_rank(content_tsv, plainto_tsquery('english', $3)) DESC
			       ) AS rank
			FROM knowledge_base
			WHERE tenant_id = $2
			  AND content_tsv @@ plainto_tsquery('english', $3)
			LIMIT 20
		),
		rrf AS (
			SELECT
				COALESCE(s.id,          f.id)          AS id,
				COALESCE(s.content,     f.content)     AS content,
				COALESCE(s.source_type, f.source_type) AS source_type,
				COALESCE(s.source_id,   f.source_id)   AS source_id,
				-- RRF: 1/(k+rank). k=60 is the standard constant from the original paper.
				-- It dampens the impact of top ranks so a single #1 rank doesn't dominate.
				COALESCE(1.0 / (60.0 + s.rank), 0.0) +
				COALESCE(1.0 / (60.0 + f.rank), 0.0) AS rrf_score
			FROM semantic_search s
			FULL OUTER JOIN fulltext_search f ON s.id = f.id
		)
		SELECT id, content, source_type, source_id, rrf_score
		FROM rrf
		ORDER BY rrf_score DESC
		LIMIT $4
	`, vec, tenantID, queryText, limit)
	if err != nil {
		return nil, fmt.Errorf("hybrid search query failed: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		var doc Document
		var sourceID *uuid.UUID
		var score float32
		if err := rows.Scan(&doc.ID, &doc.Content, &doc.SourceType, &sourceID, &score); err != nil {
			return nil, fmt.Errorf("scan document row: %w", err)
		}
		doc.TenantID = tenantID
		doc.SourceID = sourceID
		doc.Score = score
		docs = append(docs, &doc)
	}
	return docs, rows.Err()
}

// float32SliceToPgVector converts a Go []float32 to pgvector literal format.
// pgvector expects: [0.123,0.456,...] as a SQL string cast to the vector type.
func float32SliceToPgVector(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
