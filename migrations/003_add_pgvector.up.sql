-- Migration 003: Add pgvector extension and knowledge_base table for RAG pipeline.
--
-- Why pgvector instead of a dedicated vector DB (Pinecone, Weaviate)?
-- 1. We already pay for RDS Postgres — adding pgvector is zero extra infra cost.
-- 2. Hybrid SQL queries: we can JOIN notification history with vector search in one query.
-- 3. ACID guarantees: vector index stays consistent with the rest of our data.
-- 4. Simpler ops: one database, one backup strategy, one monitoring setup.
-- Tradeoff: pgvector's IVFFlat index tops out at ~50M vectors before you'd
-- need a dedicated ANN index. For a notification platform, that's far beyond
-- our scale.

-- Step 1: Enable pgvector.
-- This adds the vector type and the <=> cosine distance operator.
CREATE EXTENSION IF NOT EXISTS vector;

-- Step 2: Knowledge base table.
-- Each row is a chunk of text (notification summary, FAQ, doc) with its embedding.
-- "Chunking" strategy: one notification = one row. For large documents you'd
-- split into 256-512 token chunks with overlap for better retrieval.
CREATE TABLE IF NOT EXISTS knowledge_base (
    -- Identity
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Multi-tenancy: RAG is scoped per tenant.
    -- All searches filter by tenant_id first so tenants never see each other's data.
    tenant_id   UUID NOT NULL,

    -- The raw text that was embedded.
    -- Stored here so we can return it as a citation without a JOIN.
    content     TEXT NOT NULL,

    -- What kind of document this is.
    source_type VARCHAR(50) NOT NULL DEFAULT 'notification',  -- 'notification' | 'faq' | 'doc'

    -- Foreign key back to the original notification (nullable for non-notification sources).
    source_id   UUID,

    -- The 1536-dimensional embedding vector (OpenAI text-embedding-3-small).
    -- Each vector is 1536 × 4 bytes = ~6KB. 100K documents = ~600MB — very manageable.
    -- NOT NULL: a row without an embedding can never be retrieved by vector search,
    -- so we reject it at write time rather than letting it rot silently in the table.
    embedding   vector(1536) NOT NULL,

    -- Generated column for full-text search (hybrid retrieval).
    -- GENERATED ALWAYS AS ... STORED means Postgres automatically maintains this
    -- as content changes — we never have to remember to update it.
    content_tsv TSVECTOR GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index 1: HNSW (Hierarchical Navigable Small World) for ANN search.
--
-- Why HNSW instead of IVFFlat here?
-- IVFFlat trains its Voronoi cell centroids from the EXISTING rows. Creating it
-- in a migration (on an empty table) produces an untrained index that gives poor
-- recall until you DROP and REBUILD it after loading data — an easy production
-- footgun. HNSW builds its graph incrementally as rows are inserted, so it's
-- correct to create up-front in a migration. It also has better recall.
-- Tradeoff: HNSW uses more memory and is slower to build per-row, but for our
-- write volume (one row per delivered notification) that's negligible.
--   m = 16              — max connections per node (pgvector default, good balance)
--   ef_construction = 64 — build-time accuracy/speed tradeoff (higher = better recall)
CREATE INDEX IF NOT EXISTS idx_knowledge_base_embedding
    ON knowledge_base USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Index 2: GIN index for full-text search (the "FT" half of hybrid search).
CREATE INDEX IF NOT EXISTS idx_knowledge_base_content_tsv
    ON knowledge_base USING GIN (content_tsv);

-- Index 3: Tenant + timestamp for filtered queries.
-- The WHERE tenant_id = $1 filter in HybridSearch uses this.
CREATE INDEX IF NOT EXISTS idx_knowledge_base_tenant
    ON knowledge_base (tenant_id, created_at DESC);
