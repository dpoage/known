package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionStore implements storage.SessionRepo using PostgreSQL.
type SessionStore struct {
	pool *pgxpool.Pool
}

func (s *SessionStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.pool)
}

func (s *SessionStore) CreateSession(ctx context.Context, session *model.Session) error {
	_, err := s.conn(ctx).Exec(ctx, `
		INSERT INTO sessions (id, started_at, ended_at, scope, agent)
		VALUES ($1, $2, $3, $4, $5)
	`,
		session.ID.String(),
		session.StartedAt,
		session.EndedAt,
		nullableString(session.Scope),
		nullableString(session.Agent),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *SessionStore) EndSession(ctx context.Context, id model.ID) error {
	now := time.Now()
	tag, err := s.conn(ctx).Exec(ctx, `
		UPDATE sessions SET ended_at = $1 WHERE id = $2
	`, now, id.String())
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *SessionStore) GetSession(ctx context.Context, id model.ID) (*model.Session, error) {
	row := s.conn(ctx).QueryRow(ctx, `
		SELECT id, started_at, ended_at, scope, agent
		FROM sessions WHERE id = $1
	`, id.String())

	var (
		idStr string
		scope *string
		agent *string
	)
	session := &model.Session{}
	if err := row.Scan(&idStr, &session.StartedAt, &session.EndedAt, &scope, &agent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	parsedID, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse session id: %w", err)
	}
	session.ID = parsedID
	if scope != nil {
		session.Scope = *scope
	}
	if agent != nil {
		session.Agent = *agent
	}
	return session, nil
}

func (s *SessionStore) LogEvent(ctx context.Context, event *model.SessionEvent) error {
	entryIDsJSON, err := storage.MarshalIDSlice(event.EntryIDs)
	if err != nil {
		return fmt.Errorf("marshal entry_ids: %w", err)
	}
	edgeIDsJSON, err := storage.MarshalIDSlice(event.EdgeIDs)
	if err != nil {
		return fmt.Errorf("marshal edge_ids: %w", err)
	}

	_, err = s.conn(ctx).Exec(ctx, `
		INSERT INTO session_events (id, session_id, event_type, entry_ids, edge_ids, query, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		event.ID.String(),
		event.SessionID.String(),
		string(event.EventType),
		entryIDsJSON,
		edgeIDsJSON,
		nullableString(event.Query),
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("log event: %w", err)
	}
	return nil
}

func (s *SessionStore) ListEvents(ctx context.Context, sessionID model.ID) ([]model.SessionEvent, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT id, session_id, event_type, entry_ids, edge_ids, query, created_at
		FROM session_events
		WHERE session_id = $1
		ORDER BY created_at
	`, sessionID.String())
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []model.SessionEvent
	for rows.Next() {
		var (
			idStr     string
			sessStr   string
			eventType string
			entryIDs  *string
			edgeIDs   *string
			query     *string
			created   time.Time
		)
		if err := rows.Scan(&idStr, &sessStr, &eventType, &entryIDs, &edgeIDs, &query, &created); err != nil {
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
		eIDs, err := storage.UnmarshalIDSlice(entryIDs)
		if err != nil {
			return nil, fmt.Errorf("unmarshal entry_ids: %w", err)
		}
		edIDs, err := storage.UnmarshalIDSlice(edgeIDs)
		if err != nil {
			return nil, fmt.Errorf("unmarshal edge_ids: %w", err)
		}

		ev := model.SessionEvent{
			ID:        parsedID,
			SessionID: sessID,
			EventType: model.EventType(eventType),
			EntryIDs:  eIDs,
			EdgeIDs:   edIDs,
			CreatedAt: created,
		}
		if query != nil {
			ev.Query = *query
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *SessionStore) ListUnprocessedSessions(ctx context.Context) ([]model.Session, error) {
	rows, err := s.conn(ctx).Query(ctx, `
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
			idStr string
			scope *string
			agent *string
		)
		session := model.Session{}
		if err := rows.Scan(&idStr, &session.StartedAt, &session.EndedAt, &scope, &agent); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		parsedID, err := model.ParseID(idStr)
		if err != nil {
			return nil, fmt.Errorf("parse session id: %w", err)
		}
		session.ID = parsedID
		if scope != nil {
			session.Scope = *scope
		}
		if agent != nil {
			session.Agent = *agent
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *SessionStore) MarkProcessed(ctx context.Context, sessionID model.ID) error {
	_, err := s.conn(ctx).Exec(ctx, `
		INSERT INTO session_reinforcements (session_id, processed_at)
		VALUES ($1, $2)
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID.String(), time.Now())
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}
