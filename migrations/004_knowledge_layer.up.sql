-- Knowledge layer: structured wisdom captured during sessions.
-- Bodies live in chunks (source_type='knowledge', source_uri='knowledge:<id>').
-- Anchors live in edges (edge_type='knowledge_about').

CREATE TABLE knowledge_entries (
    id          BIGSERIAL PRIMARY KEY,
    category    TEXT NOT NULL CHECK (category IN (
                    'lesson', 'best_practice', 'convention',
                    'domain_knowledge', 'how_to', 'decision')),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    source      TEXT NOT NULL DEFAULT 'claude',
    session_id  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_entries_category_idx ON knowledge_entries(category);
CREATE INDEX knowledge_entries_tags_idx     ON knowledge_entries USING GIN(tags);
CREATE INDEX knowledge_entries_created_idx  ON knowledge_entries(created_at DESC);
