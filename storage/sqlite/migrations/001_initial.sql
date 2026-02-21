-- =============================================================================
-- edge_types: registry of valid relationship types
-- =============================================================================

CREATE TABLE IF NOT EXISTS edge_types (
    name       TEXT PRIMARY KEY,
    predefined INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT OR IGNORE INTO edge_types (name, predefined) VALUES
    ('depends-on',  1),
    ('contradicts', 1),
    ('supersedes',  1),
    ('elaborates',  1),
    ('related-to',  1);

-- =============================================================================
-- scopes: hierarchical namespace metadata
-- =============================================================================

CREATE TABLE IF NOT EXISTS scopes (
    path       TEXT PRIMARY KEY,
    meta       TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- =============================================================================
-- entries: core knowledge storage
-- =============================================================================

CREATE TABLE IF NOT EXISTS entries (
    id              TEXT PRIMARY KEY,
    content         TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    embedding       BLOB,
    embedding_dim   INTEGER,
    embedding_model TEXT,
    source_type     TEXT NOT NULL,
    source_ref      TEXT NOT NULL,
    source_meta     TEXT,
    confidence      TEXT NOT NULL DEFAULT 'inferred',
    verified_at     TEXT,
    verified_by     TEXT,
    scope           TEXT NOT NULL REFERENCES scopes(path),
    ttl_seconds     INTEGER,
    expires_at      TEXT,
    meta            TEXT,
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CONSTRAINT uq_entries_content_hash_scope UNIQUE (content_hash, scope)
);

CREATE INDEX IF NOT EXISTS idx_entries_scope ON entries (scope);
CREATE INDEX IF NOT EXISTS idx_entries_expires_at ON entries (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries (scope, created_at);

-- =============================================================================
-- edges: directed relationships between entries
-- =============================================================================

CREATE TABLE IF NOT EXISTS edges (
    id         TEXT PRIMARY KEY,
    from_id    TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    to_id      TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    type       TEXT NOT NULL REFERENCES edge_types(name),
    weight     REAL CHECK (weight IS NULL OR (weight >= 0 AND weight <= 1)),
    meta       TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CONSTRAINT edges_no_self_ref CHECK (from_id <> to_id)
);

CREATE INDEX IF NOT EXISTS idx_edges_from ON edges (from_id, type);
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges (to_id, type);
CREATE INDEX IF NOT EXISTS idx_edges_type ON edges (type);
