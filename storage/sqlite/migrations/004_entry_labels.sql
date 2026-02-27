CREATE TABLE IF NOT EXISTS entry_labels (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    label    TEXT NOT NULL,
    PRIMARY KEY (entry_id, label)
);

CREATE INDEX IF NOT EXISTS idx_entry_labels_label ON entry_labels (label);
