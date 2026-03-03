-- Session tracking for edge reinforcement signals.

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    scope TEXT,
    agent TEXT
);

CREATE TABLE session_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    event_type TEXT NOT NULL,
    entry_ids TEXT,
    edge_ids TEXT,
    query TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE session_reinforcements (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id),
    processed_at TEXT NOT NULL
);
