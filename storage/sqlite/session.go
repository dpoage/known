package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// SessionStore implements storage.SessionRepo using SQLite.
type SessionStore struct {
	db *sql.DB
}

func (s *SessionStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.db)
}

func (s *SessionStore) CreateSession(ctx context.Context, session *model.Session) error {
	_, err := s.conn(ctx).ExecContext(ctx, `
		INSERT INTO sessions (id, started_at, ended_at, scope, agent)
		VALUES (?, ?, ?, ?, ?)
	`,
		session.ID.String(),
		formatTime(session.StartedAt),
		formatNullableTime(session.EndedAt),
		nullString(session.Scope),
		nullString(session.Agent),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *SessionStore) EndSession(ctx context.Context, id model.ID) error {
	now := formatTime(time.Now())
	result, err := s.conn(ctx).ExecContext(ctx, `
		UPDATE sessions SET ended_at = ? WHERE id = ?
	`, now, id.String())
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *SessionStore) GetSession(ctx context.Context, id model.ID) (*model.Session, error) {
	row := s.conn(ctx).QueryRowContext(ctx, `
		SELECT id, started_at, ended_at, scope, agent
		FROM sessions WHERE id = ?
	`, id.String())

	var (
		idStr   string
		started string
		ended   *string
		scope   sql.NullString
		agent   sql.NullString
	)
	if err := row.Scan(&idStr, &started, &ended, &scope, &agent); err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	parsedID, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse session id: %w", err)
	}

	session := &model.Session{
		ID:        parsedID,
		StartedAt: parseTime(started),
		Scope:     scope.String,
		Agent:     agent.String,
	}
	if ended != nil {
		t := parseTime(*ended)
		session.EndedAt = &t
	}
	return session, nil
}

func (s *SessionStore) LogEvent(ctx context.Context, event *model.SessionEvent) error {
	entryIDsJSON, err := marshalIDSlice(event.EntryIDs)
	if err != nil {
		return fmt.Errorf("marshal entry_ids: %w", err)
	}
	edgeIDsJSON, err := marshalIDSlice(event.EdgeIDs)
	if err != nil {
		return fmt.Errorf("marshal edge_ids: %w", err)
	}

	_, err = s.conn(ctx).ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, event_type, entry_ids, edge_ids, query, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		event.ID.String(),
		event.SessionID.String(),
		string(event.EventType),
		entryIDsJSON,
		edgeIDsJSON,
		nullString(event.Query),
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("log event: %w", err)
	}
	return nil
}

func (s *SessionStore) ListEvents(ctx context.Context, sessionID model.ID) ([]model.SessionEvent, error) {
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT id, session_id, event_type, entry_ids, edge_ids, query, created_at
		FROM session_events
		WHERE session_id = ?
		ORDER BY created_at
	`, sessionID.String())
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []model.SessionEvent
	for rows.Next() {
		ev, err := scanSessionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *ev)
	}
	return events, rows.Err()
}

func (s *SessionStore) ListUnprocessedSessions(ctx context.Context) ([]model.Session, error) {
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT s.id, s.started_at, s.ended_at, s.scope, s.agent
		FROM sessions s
		WHERE s.ended_at IS NOT NULL
		  AND s.id NOT IN (SELECT session_id FROM session_reinforcements)
		ORDER BY s.started_at
	`)
	if err != nil {
		return nil, fmt.Errorf("list unprocessed sessions: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		var (
			idStr   string
			started string
			ended   *string
			scope   sql.NullString
			agent   sql.NullString
		)
		if err := rows.Scan(&idStr, &started, &ended, &scope, &agent); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		parsedID, err := model.ParseID(idStr)
		if err != nil {
			return nil, fmt.Errorf("parse session id: %w", err)
		}
		session := model.Session{
			ID:        parsedID,
			StartedAt: parseTime(started),
			Scope:     scope.String,
			Agent:     agent.String,
		}
		if ended != nil {
			t := parseTime(*ended)
			session.EndedAt = &t
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *SessionStore) MarkProcessed(ctx context.Context, sessionID model.ID) error {
	_, err := s.conn(ctx).ExecContext(ctx, `
		INSERT INTO session_reinforcements (session_id, processed_at)
		VALUES (?, ?)
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID.String(), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}

// Helper functions

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func marshalIDSlice(ids []model.ID) (*string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	b, err := json.Marshal(strs)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

func unmarshalIDSlice(data *string) ([]model.ID, error) {
	if data == nil || *data == "" {
		return nil, nil
	}
	var strs []string
	if err := json.Unmarshal([]byte(*data), &strs); err != nil {
		return nil, err
	}
	ids := make([]model.ID, len(strs))
	for i, s := range strs {
		id, err := model.ParseID(s)
		if err != nil {
			return nil, fmt.Errorf("parse id %q: %w", s, err)
		}
		ids[i] = id
	}
	return ids, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSessionEvent(row scannable) (*model.SessionEvent, error) {
	var (
		idStr     string
		sessStr   string
		eventType string
		entryIDs  *string
		edgeIDs   *string
		query     sql.NullString
		created   string
	)

	if err := row.Scan(&idStr, &sessStr, &eventType, &entryIDs, &edgeIDs, &query, &created); err != nil {
		return nil, fmt.Errorf("scan event: %w", err)
	}

	parsedID, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse event id: %w", err)
	}
	sessID, err := model.ParseID(sessStr)
	if err != nil {
		return nil, fmt.Errorf("parse session id: %w", err)
	}
	eIDs, err := unmarshalIDSlice(entryIDs)
	if err != nil {
		return nil, fmt.Errorf("unmarshal entry_ids: %w", err)
	}
	edIDs, err := unmarshalIDSlice(edgeIDs)
	if err != nil {
		return nil, fmt.Errorf("unmarshal edge_ids: %w", err)
	}

	return &model.SessionEvent{
		ID:        parsedID,
		SessionID: sessID,
		EventType: model.EventType(eventType),
		EntryIDs:  eIDs,
		EdgeIDs:   edIDs,
		Query:     query.String,
		CreatedAt: parseTime(created),
	}, nil
}
