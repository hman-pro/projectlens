-- Change vector dimensions from 3072 (OpenAI) to 1024 (Ollama mxbai-embed-large).
-- Existing embeddings are incompatible and must be regenerated.
DROP INDEX IF EXISTS idx_embeddings_hnsw;
TRUNCATE embeddings;
ALTER TABLE embeddings ALTER COLUMN embedding TYPE halfvec(1024);
CREATE INDEX idx_embeddings_hnsw ON embeddings
  USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
