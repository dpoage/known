-- Add index on session_events.session_id for efficient event lookups.
CREATE INDEX idx_session_events_session_id ON session_events(session_id);
