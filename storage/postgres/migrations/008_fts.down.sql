DROP INDEX IF EXISTS entries_search_vec_gin;
ALTER TABLE entries DROP COLUMN IF EXISTS search_vec;
