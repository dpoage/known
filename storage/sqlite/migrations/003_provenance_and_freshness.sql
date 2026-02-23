-- Add freshness tracking columns.
-- The confidence column remains in place (SQLite can't reliably drop columns)
-- but is semantically renamed to "provenance" in the Go layer.

ALTER TABLE entries ADD COLUMN observed_at TEXT;
ALTER TABLE entries ADD COLUMN observed_by TEXT;
ALTER TABLE entries ADD COLUMN source_hash TEXT;

-- Backfill observed_at from verified_at (if set) or created_at.
UPDATE entries SET
    observed_at = COALESCE(verified_at, created_at),
    observed_by = CASE
        WHEN verified_by IS NOT NULL AND verified_by != '' THEN verified_by
        WHEN source_type = 'conversation' OR source_type = 'manual' THEN 'user'
        ELSE 'agent'
    END;
