-- Restore verified_at and verified_by from freshness columns.
ALTER TABLE entries ADD COLUMN verified_at TIMESTAMPTZ;
ALTER TABLE entries ADD COLUMN verified_by TEXT;

UPDATE entries SET
    verified_at = observed_at,
    verified_by = observed_by;

ALTER TABLE entries DROP COLUMN observed_at;
ALTER TABLE entries DROP COLUMN observed_by;
ALTER TABLE entries DROP COLUMN source_hash;
