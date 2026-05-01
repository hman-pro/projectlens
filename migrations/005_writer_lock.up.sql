CREATE TABLE index_locks (
    id          SERIAL PRIMARY KEY,
    lock_id     BIGINT NOT NULL,
    client_pid  INTEGER NOT NULL,
    backend_pid INTEGER NOT NULL,
    hostname    TEXT NOT NULL,
    cmd         TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (lock_id)
);

CREATE INDEX idx_index_locks_backend_pid ON index_locks(backend_pid);
