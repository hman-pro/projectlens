-- Constrain provenance to the vocabulary documented in
-- docs/2026-05-22-confidence-and-provenance-design.md.
-- Open to extension: add a producer (e.g. 'pr', 'lightrag') in a follow-up
-- migration when its writer lands.

ALTER TABLE edges
    DROP CONSTRAINT IF EXISTS edges_provenance_check;

ALTER TABLE edges
    ADD CONSTRAINT edges_provenance_check
        CHECK (provenance IS NULL OR provenance IN (
            'parser',
            'callgraph',
            'sql_scanner',
            'history',
            'knowledge',
            'docs'
        ));
