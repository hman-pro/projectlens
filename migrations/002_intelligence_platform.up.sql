-- 1. Extend symbols with SCIP-style ID and role bitset
ALTER TABLE symbols ADD COLUMN scip_symbol TEXT;
ALTER TABLE symbols ADD COLUMN roles INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_symbols_scip ON symbols(scip_symbol);

-- 2. Extend chunks to support multiple content types
ALTER TABLE chunks ALTER COLUMN symbol_id DROP NOT NULL;
ALTER TABLE chunks ADD COLUMN source_type TEXT NOT NULL DEFAULT 'code';
ALTER TABLE chunks ADD COLUMN source_uri TEXT;
CREATE INDEX idx_chunks_source_type ON chunks(source_type);

-- 3. Refactor edges to polymorphic graph model
--    Drop old edges table and recreate with new schema.
--    Edges are derived data (rebuilt from code), safe to drop.
DROP TABLE IF EXISTS edges;
CREATE TABLE edges (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       BIGINT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       BIGINT NOT NULL,
    edge_type       TEXT NOT NULL,
    properties      JSONB,
    confidence      REAL,
    UNIQUE (source_type, source_id, target_type, target_id, edge_type)
);
CREATE INDEX idx_edges_source ON edges(source_type, source_id);
CREATE INDEX idx_edges_target ON edges(target_type, target_id);
CREATE INDEX idx_edges_type ON edges(edge_type);

-- 4. New table: datastore_tables
CREATE TABLE datastore_tables (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    engine          TEXT NOT NULL,
    schema_name     TEXT,
    columns         JSONB,
    source_file_id  BIGINT REFERENCES files(id) ON DELETE SET NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, engine)
);

-- 5. New table: documents
CREATE TABLE documents (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    title           TEXT NOT NULL,
    url             TEXT,
    body_text       TEXT,
    last_synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata        JSONB,
    UNIQUE (source_type, external_id)
);

-- 6. New table: symbol_history
CREATE TABLE symbol_history (
    id              BIGSERIAL PRIMARY KEY,
    symbol_id       BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    commit_hash     TEXT NOT NULL,
    author          TEXT NOT NULL,
    committed_at    TIMESTAMPTZ NOT NULL,
    change_type     TEXT NOT NULL,
    diff_snippet    TEXT,
    UNIQUE (symbol_id, commit_hash)
);
CREATE INDEX idx_symbol_history_symbol ON symbol_history(symbol_id);
CREATE INDEX idx_symbol_history_commit ON symbol_history(committed_at);

-- 7. New table: file_history
CREATE TABLE file_history (
    id              BIGSERIAL PRIMARY KEY,
    file_id         BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    commit_hash     TEXT NOT NULL,
    author          TEXT NOT NULL,
    committed_at    TIMESTAMPTZ NOT NULL,
    change_type     TEXT NOT NULL,
    diff_snippet    TEXT,
    UNIQUE (file_id, commit_hash)
);
CREATE INDEX idx_file_history_file ON file_history(file_id);
CREATE INDEX idx_file_history_commit ON file_history(committed_at);

-- 8. Extend index_runs to track per-stage status
ALTER TABLE index_runs ADD COLUMN stage TEXT NOT NULL DEFAULT 'code';
