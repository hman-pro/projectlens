ALTER TABLE edges
    ADD COLUMN IF NOT EXISTS provenance TEXT,
    ADD COLUMN IF NOT EXISTS confidence_class TEXT;

ALTER TABLE edges
    DROP CONSTRAINT IF EXISTS edges_confidence_class_check;

ALTER TABLE edges
    ADD CONSTRAINT edges_confidence_class_check
        CHECK (confidence_class IS NULL OR confidence_class IN ('extracted', 'inferred', 'ambiguous'));

CREATE INDEX IF NOT EXISTS idx_edges_provenance
    ON edges(provenance)
    WHERE provenance IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_edges_confidence_class
    ON edges(confidence_class)
    WHERE confidence_class IS NOT NULL;
