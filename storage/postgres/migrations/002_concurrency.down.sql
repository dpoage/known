-- Remove unique constraint on content hash + scope
ALTER TABLE entries DROP CONSTRAINT IF EXISTS uq_entries_content_hash_scope;

-- Remove content_hash column
ALTER TABLE entries DROP COLUMN IF EXISTS content_hash;

-- Remove version column
ALTER TABLE entries DROP COLUMN IF EXISTS version;
