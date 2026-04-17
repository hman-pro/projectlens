-- Revert to 3072-dim vectors (OpenAI text-embedding-3-large).
-- Existing embeddings are incompatible and must be regenerated.
DROP INDEX IF EXISTS idx_embeddings_hnsw;
TRUNCATE embeddings;
ALTER TABLE embeddings ALTER COLUMN embedding TYPE halfvec(3072);
CREATE INDEX idx_embeddings_hnsw ON embeddings
  USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
