-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS ltree;

-- =============================================================================
-- edge_types: registry of valid relationship types
-- =============================================================================

CREATE TABLE edge_types (
    name       TEXT PRIMARY KEY,
    predefined BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed predefined edge types
INSERT INTO edge_types (name, predefined) VALUES
    ('depends-on',  TRUE),
    ('contradicts', TRUE),
    ('supersedes',  TRUE),
    ('elaborates',  TRUE),
    ('related-to',  TRUE);

-- =============================================================================
-- scopes: hierarchical namespace metadata
-- =============================================================================

CREATE TABLE scopes (
    path       TEXT PRIMARY KEY,                 -- dot-separated path (e.g. "project.auth.oauth")
    ltree_path LTREE NOT NULL,                   -- ltree representation for hierarchical queries
    meta       JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scopes_ltree ON scopes USING GIST (ltree_path);

-- =============================================================================
-- entries: core knowledge storage
-- =============================================================================

CREATE TABLE entries (
    id              TEXT PRIMARY KEY,            -- ULID
    content         TEXT NOT NULL,
    embedding       vector,                      -- pgvector; dimension varies per embedding model
    embedding_dim   INTEGER,
    embedding_model TEXT,
    source_type     TEXT NOT NULL,
    source_ref      TEXT NOT NULL,
    source_meta     JSONB,
    confidence      TEXT NOT NULL DEFAULT 'inferred',
    verified_at     TIMESTAMPTZ,
    verified_by     TEXT,
    scope           TEXT NOT NULL REFERENCES scopes(path),
    ttl_seconds     BIGINT,                      -- TTL stored as seconds for portability
    expires_at      TIMESTAMPTZ,
    meta            JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial index for active (non-expired) entries
CREATE INDEX idx_entries_active ON entries (scope, created_at)
    WHERE expires_at IS NULL OR expires_at > NOW();

-- Index for TTL expiration cleanup
CREATE INDEX idx_entries_expires_at ON entries (expires_at)
    WHERE expires_at IS NOT NULL;

-- GIN index for metadata queries
CREATE INDEX idx_entries_meta ON entries USING GIN (meta jsonb_path_ops);

-- Index for scope lookups
CREATE INDEX idx_entries_scope ON entries (scope);

-- =============================================================================
-- edges: directed relationships between entries
-- =============================================================================

CREATE TABLE edges (
    id         TEXT PRIMARY KEY,                 -- ULID
    from_id    TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    to_id      TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    type       TEXT NOT NULL REFERENCES edge_types(name),
    weight     DOUBLE PRECISION CHECK (weight IS NULL OR (weight >= 0 AND weight <= 1)),
    meta       JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT edges_no_self_ref CHECK (from_id <> to_id)
);

CREATE INDEX idx_edges_from ON edges (from_id, type);
CREATE INDEX idx_edges_to   ON edges (to_id, type);
CREATE INDEX idx_edges_type ON edges (type);
