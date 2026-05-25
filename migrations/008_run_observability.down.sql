ALTER TABLE index_runs
    DROP COLUMN IF EXISTS metrics,
    DROP COLUMN IF EXISTS provider_summarize,
    DROP COLUMN IF EXISTS provider_embed,
    DROP COLUMN IF EXISTS error_text;
