-- FTS5 full-text search index on entries (external content, backed by entries table).
CREATE VIRTUAL TABLE entries_fts USING fts5(
    title, content,
    content=entries, content_rowid=rowid
);

-- Sync triggers to keep FTS index in sync with entries table.
CREATE TRIGGER entries_fts_ai AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;

CREATE TRIGGER entries_fts_ad AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, content) VALUES('delete', old.rowid, old.title, old.content);
END;

CREATE TRIGGER entries_fts_au AFTER UPDATE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, content) VALUES('delete', old.rowid, old.title, old.content);
    INSERT INTO entries_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;

-- Backfill existing entries into the FTS index.
INSERT INTO entries_fts(rowid, title, content) SELECT rowid, title, content FROM entries;
