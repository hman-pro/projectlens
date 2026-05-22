DROP INDEX IF EXISTS idx_edges_confidence_class;
DROP INDEX IF EXISTS idx_edges_provenance;

ALTER TABLE edges
    DROP CONSTRAINT IF EXISTS edges_confidence_class_check;

ALTER TABLE edges
    DROP COLUMN IF EXISTS confidence_class,
    DROP COLUMN IF EXISTS provenance;
