-- Add freshness tracking columns.
ALTER TABLE entries ADD COLUMN observed_at TIMESTAMPTZ;
ALTER TABLE entries ADD COLUMN observed_by TEXT;
ALTER TABLE entries ADD COLUMN source_hash TEXT;

-- Backfill observed_at from verified_at (if set) or created_at.
UPDATE entries SET
    observed_at = COALESCE(verified_at, created_at),
    observed_by = CASE
        WHEN verified_by IS NOT NULL AND verified_by != '' THEN verified_by
        WHEN source_type IN ('conversation', 'manual') THEN 'user'
        ELSE 'agent'
    END;

-- Drop old columns that are now captured by Freshness.
ALTER TABLE entries DROP COLUMN verified_at;
ALTER TABLE entries DROP COLUMN verified_by;
