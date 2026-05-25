-- Add run observability columns to index_runs per
-- docs/2026-05-23-run-observability-design.md.
-- Records error text, active provider identifiers, and a flexible metrics bag.

ALTER TABLE index_runs
    ADD COLUMN error_text         TEXT,
    ADD COLUMN provider_embed     TEXT,
    ADD COLUMN provider_summarize TEXT,
    ADD COLUMN metrics            JSONB NOT NULL DEFAULT '{}'::jsonb;
