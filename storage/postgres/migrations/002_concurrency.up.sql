-- Add optimistic concurrency control version column
ALTER TABLE entries ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

-- Add content hash for duplicate detection
ALTER TABLE entries ADD COLUMN content_hash TEXT;

-- Backfill content_hash for existing rows using built-in sha256 (PostgreSQL 14+)
UPDATE entries SET content_hash = encode(sha256(content::bytea), 'hex')
WHERE content_hash IS NULL;

-- Now make content_hash NOT NULL after backfill
ALTER TABLE entries ALTER COLUMN content_hash SET NOT NULL;

-- Add unique constraint on (content_hash, scope) to prevent duplicate knowledge
ALTER TABLE entries ADD CONSTRAINT uq_entries_content_hash_scope UNIQUE (content_hash, scope);
