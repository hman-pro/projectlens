-- 009 — Context graph: sources, people, items, versions, chunks, participants.
-- Spec: docs/superpowers/specs/2026-05-25-context-graph-data-model-design.md

CREATE TABLE context_sources (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    base_url        TEXT,
    external_key    TEXT NOT NULL,
    config_hash     TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_type, external_key)
);

CREATE INDEX idx_context_sources_source_type ON context_sources(source_type);

CREATE TABLE context_source_state (
    id                      BIGSERIAL PRIMARY KEY,
    source_id               BIGINT NOT NULL REFERENCES context_sources(id) ON DELETE CASCADE,
    stream                  TEXT NOT NULL,
    cursor_kind             TEXT NOT NULL,
    cursor_value            TEXT,
    last_successful_run_id  BIGINT REFERENCES index_runs(id) ON DELETE SET NULL,
    last_successful_at      TIMESTAMPTZ,
    metadata                JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (source_id, stream)
);

CREATE TABLE people (
    id                  BIGSERIAL PRIMARY KEY,
    display_name        TEXT,
    primary_email_hash  TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_people_primary_email_hash ON people(primary_email_hash) WHERE primary_email_hash IS NOT NULL;

CREATE TABLE person_identities (
    id                  BIGSERIAL PRIMARY KEY,
    person_id           BIGINT REFERENCES people(id) ON DELETE SET NULL,
    provider            TEXT NOT NULL,
    external_account_id TEXT NOT NULL,
    username            TEXT,
    display_name        TEXT,
    email_hash          TEXT,
    profile_url         TEXT,
    confidence_class    TEXT NOT NULL DEFAULT 'extracted',
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, external_account_id)
);

CREATE INDEX idx_person_identities_person ON person_identities(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX idx_person_identities_email_hash ON person_identities(email_hash) WHERE email_hash IS NOT NULL;

CREATE TABLE context_items (
    id              BIGSERIAL PRIMARY KEY,
    source_id       BIGINT NOT NULL REFERENCES context_sources(id) ON DELETE CASCADE,
    item_type       TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    parent_item_id  BIGINT REFERENCES context_items(id) ON DELETE SET NULL,
    root_item_id    BIGINT REFERENCES context_items(id) ON DELETE SET NULL,
    url             TEXT,
    title           TEXT,
    state           TEXT,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_id, item_type, external_id)
);

CREATE INDEX idx_context_items_root ON context_items(root_item_id) WHERE root_item_id IS NOT NULL;
CREATE INDEX idx_context_items_parent ON context_items(parent_item_id) WHERE parent_item_id IS NOT NULL;
CREATE INDEX idx_context_items_type ON context_items(item_type);

CREATE TABLE context_item_versions (
    id                BIGSERIAL PRIMARY KEY,
    item_id           BIGINT NOT NULL REFERENCES context_items(id) ON DELETE CASCADE,
    external_version  TEXT,
    content_hash      TEXT NOT NULL,
    body_text         TEXT NOT NULL,
    redaction         JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_current        BOOLEAN NOT NULL DEFAULT TRUE,
    inserted_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at     TIMESTAMPTZ,
    run_id            BIGINT REFERENCES index_runs(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX context_item_versions_current_idx
    ON context_item_versions(item_id)
    WHERE is_current = TRUE;

CREATE INDEX context_item_versions_lineage_idx
    ON context_item_versions(item_id, inserted_at DESC);

CREATE TABLE context_chunks (
    id                  BIGSERIAL PRIMARY KEY,
    item_version_id     BIGINT NOT NULL REFERENCES context_item_versions(id) ON DELETE CASCADE,
    chunk_key           TEXT NOT NULL,
    chunk_anchor_id     TEXT NOT NULL,
    source_anchor_id    TEXT NOT NULL,
    chunk_index         INTEGER NOT NULL,
    heading             TEXT,
    content_hash        TEXT NOT NULL,
    token_count         INTEGER NOT NULL DEFAULT 0,
    chunk_id            BIGINT REFERENCES chunks(id) ON DELETE SET NULL,
    lightrag_doc_id     TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (item_version_id, chunk_key),
    UNIQUE (chunk_anchor_id)
);

CREATE INDEX idx_context_chunks_source_anchor ON context_chunks(source_anchor_id);
CREATE INDEX idx_context_chunks_lightrag_doc ON context_chunks(lightrag_doc_id) WHERE lightrag_doc_id IS NOT NULL;

CREATE TABLE context_participants (
    id              BIGSERIAL PRIMARY KEY,
    item_id         BIGINT NOT NULL REFERENCES context_items(id) ON DELETE CASCADE,
    person_id       BIGINT REFERENCES people(id) ON DELETE SET NULL,
    identity_id     BIGINT REFERENCES person_identities(id) ON DELETE SET NULL,
    role            TEXT NOT NULL,
    source_role     TEXT NOT NULL DEFAULT '',
    occurred_at     TIMESTAMPTZ,
    is_current      BOOLEAN NOT NULL DEFAULT TRUE,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT context_participants_who_present
        CHECK (person_id IS NOT NULL OR identity_id IS NOT NULL),
    CONSTRAINT context_participants_uniq
        UNIQUE NULLS NOT DISTINCT (item_id, identity_id, person_id, role, source_role)
);

CREATE INDEX idx_context_participants_item ON context_participants(item_id);
CREATE INDEX idx_context_participants_person ON context_participants(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX idx_context_participants_identity ON context_participants(identity_id) WHERE identity_id IS NOT NULL;
