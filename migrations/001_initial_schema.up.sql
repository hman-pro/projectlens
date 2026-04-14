CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE files (
    id              BIGSERIAL PRIMARY KEY,
    path            TEXT NOT NULL,
    package_name    TEXT NOT NULL,
    checksum        TEXT NOT NULL,
    language        TEXT NOT NULL DEFAULT 'go',
    is_generated    BOOLEAN NOT NULL DEFAULT FALSE,
    is_test         BOOLEAN NOT NULL DEFAULT FALSE,
    line_count      INTEGER NOT NULL DEFAULT 0,
    heuristic_summary TEXT,
    commit_sha      TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (path)
);

CREATE TABLE symbols (
    id              BIGSERIAL PRIMARY KEY,
    file_id         BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL,
    package_name    TEXT NOT NULL,
    receiver        TEXT,
    signature       TEXT NOT NULL,
    doc_comment     TEXT,
    line_start      INTEGER NOT NULL,
    line_end        INTEGER NOT NULL,
    checksum        TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_symbols_name ON symbols(name);
CREATE INDEX idx_symbols_package ON symbols(package_name);
CREATE INDEX idx_symbols_kind ON symbols(kind);
CREATE INDEX idx_symbols_file_id ON symbols(file_id);

CREATE TABLE chunks (
    id              BIGSERIAL PRIMARY KEY,
    symbol_id       BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    token_count     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (symbol_id)
);

CREATE TABLE embeddings (
    id              BIGSERIAL PRIMARY KEY,
    chunk_id        BIGINT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    model_version   TEXT NOT NULL,
    embedding       halfvec(3072),
    UNIQUE (chunk_id, model_version)
);
CREATE INDEX idx_embeddings_hnsw ON embeddings USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);

CREATE TABLE summaries (
    id              BIGSERIAL PRIMARY KEY,
    package_name    TEXT NOT NULL,
    summary_text    TEXT NOT NULL,
    model_version   TEXT NOT NULL,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (package_name)
);

CREATE TABLE edges (
    id                  BIGSERIAL PRIMARY KEY,
    source_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    target_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    edge_type           TEXT NOT NULL,
    UNIQUE (source_symbol_id, target_symbol_id, edge_type)
);
CREATE INDEX idx_edges_source ON edges(source_symbol_id);
CREATE INDEX idx_edges_target ON edges(target_symbol_id);
CREATE INDEX idx_edges_type ON edges(edge_type);

CREATE TABLE index_runs (
    id              BIGSERIAL PRIMARY KEY,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    commit_sha      TEXT NOT NULL,
    files_processed INTEGER NOT NULL DEFAULT 0,
    symbols_extracted INTEGER NOT NULL DEFAULT 0,
    edges_created   INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'running'
);

CREATE TABLE git_refs (
    id              BIGSERIAL PRIMARY KEY,
    branch          TEXT NOT NULL,
    commit_sha      TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch)
);
