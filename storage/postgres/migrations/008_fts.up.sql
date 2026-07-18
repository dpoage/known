-- Add tsvector column for full-text search on entries.
-- We store a precomputed tsvector (english config, title weighted A, content weighted B)
-- and keep it in sync via triggers. A GIN index makes searches fast.

ALTER TABLE entries
    ADD COLUMN IF NOT EXISTS search_vec tsvector
        GENERATED ALWAYS AS (
            setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
            setweight(to_tsvector('english', coalesce(content, '')), 'B')
        ) STORED;

CREATE INDEX IF NOT EXISTS entries_search_vec_gin ON entries USING GIN (search_vec);
