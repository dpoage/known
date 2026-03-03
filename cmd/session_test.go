package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// stubSessionRepo is a minimal in-memory SessionRepo for testing.
type stubSessionRepo struct {
	sessions map[string]*model.Session
	events   []model.SessionEvent
	processed map[string]bool
}

func newStubSessionRepo() *stubSessionRepo {
	return &stubSessionRepo{
		sessions:  make(map[string]*model.Session),
		processed: make(map[string]bool),
	}
}

func (s *stubSessionRepo) CreateSession(_ context.Context, session *model.Session) error {
	clone := *session
	s.sessions[session.ID.String()] = &clone
	return nil
}

func (s *stubSessionRepo) EndSession(_ context.Context, id model.ID) error {
	sess, ok := s.sessions[id.String()]
	if !ok {
		return storage.ErrNotFound
	}
	now := time.Now()
	sess.EndedAt = &now
	return nil
}

func (s *stubSessionRepo) GetSession(_ context.Context, id model.ID) (*model.Session, error) {
	sess, ok := s.sessions[id.String()]
	if !ok {
		return nil, storage.ErrNotFound
	}
	clone := *sess
	return &clone, nil
}

func (s *stubSessionRepo) LogEvent(_ context.Context, event *model.SessionEvent) error {
	clone := *event
	s.events = append(s.events, clone)
	return nil
}

func (s *stubSessionRepo) ListEvents(_ context.Context, sessionID model.ID) ([]model.SessionEvent, error) {
	var result []model.SessionEvent
	for _, ev := range s.events {
		if ev.SessionID == sessionID {
			result = append(result, ev)
		}
	}
	return result, nil
}

func (s *stubSessionRepo) ListUnprocessedSessions(_ context.Context) ([]model.Session, error) {
	var result []model.Session
	for _, sess := range s.sessions {
		if sess.EndedAt != nil && !s.processed[sess.ID.String()] {
			result = append(result, *sess)
		}
	}
	return result, nil
}

func (s *stubSessionRepo) MarkProcessed(_ context.Context, sessionID model.ID) error {
	s.processed[sessionID.String()] = true
	return nil
}

var _ storage.SessionRepo = (*stubSessionRepo)(nil)

func TestRunSession_StartCreatesSession(t *testing.T) {
	// Override HOME to prevent reading a real ~/.known/session file.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KNOWN_SESSION", "")

	sessions := newStubSessionRepo()
	var buf bytes.Buffer
	app := &App{
		Sessions: sessions,
		Printer:  NewPrinter(&buf, false, false),
		Config: &AppConfig{
			DefaultScope: "test",
		},
	}

	err := runSession(context.Background(), app, []string{"start"})
	if err != nil {
		t.Fatalf("session start: %v", err)
	}

	if len(sessions.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions.sessions))
	}

	// Verify session was created with correct scope.
	for _, sess := range sessions.sessions {
		if sess.Scope != "test" {
			t.Errorf("scope = %q, want %q", sess.Scope, "test")
		}
	}
}

func TestRunSession_EndTerminatesSession(t *testing.T) {
	sessions := newStubSessionRepo()

	// Create a session directly in the stub.
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
		Scope:     "test",
	}

	// Set env var so readSessionID returns this session.
	t.Setenv("KNOWN_SESSION", sessID.String())

	var buf bytes.Buffer
	app := &App{
		Sessions: sessions,
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: "test"},
	}

	err := runSession(context.Background(), app, []string{"end"})
	if err != nil {
		t.Fatalf("session end: %v", err)
	}

	// Verify session was ended.
	sess := sessions.sessions[sessID.String()]
	if sess.EndedAt == nil {
		t.Error("session should have ended_at set")
	}
}

func TestLogSessionEvent_RecallLogsQuery(t *testing.T) {
	sessions := newStubSessionRepo()
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
	}

	var buf bytes.Buffer
	app := &App{
		Sessions:  sessions,
		SessionID: sessID.String(),
		Printer:   NewPrinter(&buf, false, true),
		Config:    &AppConfig{DefaultScope: "test"},
	}

	logSessionEvent(context.Background(), app, "recall", []string{"test query"})

	if len(sessions.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sessions.events))
	}

	ev := sessions.events[0]
	if ev.EventType != model.EventRecall {
		t.Errorf("event type = %s, want recall", ev.EventType)
	}
	if ev.Query != "test query" {
		t.Errorf("query = %q, want %q", ev.Query, "test query")
	}
	if ev.SessionID != sessID {
		t.Errorf("session id mismatch")
	}
}

func TestLogSessionEvent_ShowLogsEntryID(t *testing.T) {
	sessions := newStubSessionRepo()
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
	}

	entryID := model.NewID()
	var buf bytes.Buffer
	app := &App{
		Sessions:  sessions,
		SessionID: sessID.String(),
		Printer:   NewPrinter(&buf, false, true),
		Config:    &AppConfig{DefaultScope: "test"},
	}

	logSessionEvent(context.Background(), app, "show", []string{entryID.String()})

	if len(sessions.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sessions.events))
	}

	ev := sessions.events[0]
	if ev.EventType != model.EventShow {
		t.Errorf("event type = %s, want show", ev.EventType)
	}
	if len(ev.EntryIDs) != 1 || ev.EntryIDs[0] != entryID {
		t.Errorf("entry_ids = %v, want [%s]", ev.EntryIDs, entryID)
	}
}

func TestLogSessionEvent_NoSessionNoop(t *testing.T) {
	sessions := newStubSessionRepo()
	var buf bytes.Buffer
	app := &App{
		Sessions:  sessions,
		SessionID: "", // no active session
		Printer:   NewPrinter(&buf, false, true),
		Config:    &AppConfig{DefaultScope: "test"},
	}

	logSessionEvent(context.Background(), app, "recall", []string{"test"})

	if len(sessions.events) != 0 {
		t.Errorf("expected no events, got %d", len(sessions.events))
	}
}

func TestLogSessionEvent_UnknownCommandNoop(t *testing.T) {
	sessions := newStubSessionRepo()
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
	}

	var buf bytes.Buffer
	app := &App{
		Sessions:  sessions,
		SessionID: sessID.String(),
		Printer:   NewPrinter(&buf, false, true),
		Config:    &AppConfig{DefaultScope: "test"},
	}

	logSessionEvent(context.Background(), app, "stats", []string{})

	if len(sessions.events) != 0 {
		t.Errorf("expected no events for stats, got %d", len(sessions.events))
	}
}

func TestCommandToEventType(t *testing.T) {
	tests := []struct {
		cmd  string
		want model.EventType
	}{
		{"recall", model.EventRecall},
		{"search", model.EventSearch},
		{"show", model.EventShow},
		{"add", model.EventAdd},
		{"update", model.EventUpdate},
		{"link", model.EventLink},
		{"delete", model.EventDelete},
		{"stats", ""},
		{"gc", ""},
	}

	for _, tt := range tests {
		got := commandToEventType(tt.cmd)
		if got != tt.want {
			t.Errorf("commandToEventType(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestExtractQuery(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"test query"}, "test query"},
		{[]string{"--scope", "foo", "query text"}, "query text"},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		got := extractQuery(tt.args)
		if got != tt.want {
			t.Errorf("extractQuery(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
