-- Remove new tables
DROP TABLE IF EXISTS file_history;
DROP TABLE IF EXISTS symbol_history;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS datastore_tables;

-- Restore original edges table
DROP TABLE IF EXISTS edges;
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

-- Remove added columns from chunks
ALTER TABLE chunks DROP COLUMN IF EXISTS source_uri;
ALTER TABLE chunks DROP COLUMN IF EXISTS source_type;
ALTER TABLE chunks ALTER COLUMN symbol_id SET NOT NULL;

-- Remove added columns from symbols
DROP INDEX IF EXISTS idx_symbols_scip;
ALTER TABLE symbols DROP COLUMN IF EXISTS roles;
ALTER TABLE symbols DROP COLUMN IF EXISTS scip_symbol;

-- Remove added column from index_runs
ALTER TABLE index_runs DROP COLUMN IF EXISTS stage;
