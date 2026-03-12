package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// stubSessionRepo is a minimal in-memory SessionRepo for testing.
type stubSessionRepo struct {
	sessions  map[string]*model.Session
	events    []model.SessionEvent
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

// stubBackend provides a pass-through WithTx for tests.
type stubBackend struct {
	sessions storage.SessionRepo
	edges    storage.EdgeRepo
}

func (b *stubBackend) Entries() storage.EntryRepo    { return nil }
func (b *stubBackend) Edges() storage.EdgeRepo       { return b.edges }
func (b *stubBackend) Scopes() storage.ScopeRepo     { return nil }
func (b *stubBackend) Sessions() storage.SessionRepo { return b.sessions }
func (b *stubBackend) Labels() storage.LabelLister   { return &stubLabelLister{} }

// stubLabelLister is a no-op LabelLister for tests.
type stubLabelLister struct{}

func (s *stubLabelLister) ListLabels(_ context.Context) ([]string, error) { return nil, nil }
func (b *stubBackend) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func (b *stubBackend) Close() error   { return nil }
func (b *stubBackend) Migrate() error { return nil }

var _ storage.Backend = (*stubBackend)(nil)

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
	edges := &stubEdgeRepo{}
	db := &stubBackend{sessions: sessions, edges: edges}

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
		DB:       db,
		Sessions: sessions,
		Edges:    edges,
		Engine:   query.New(nil, edges, nil),
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

func TestRunSession_EndRunsReinforcement(t *testing.T) {
	sessions := newStubSessionRepo()
	edges := &stubEdgeRepo{}
	db := &stubBackend{sessions: sessions, edges: edges}

	// Create a session with recall-then-show events (triggers reinforcement).
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
		Scope:     "test",
	}

	entryID := model.NewID()
	sessions.events = []model.SessionEvent{
		{ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall, Query: "test", CreatedAt: time.Now()},
		{ID: model.NewID(), SessionID: sessID, EventType: model.EventShow, EntryIDs: []model.ID{entryID}, CreatedAt: time.Now()},
	}

	t.Setenv("KNOWN_SESSION", sessID.String())

	var buf bytes.Buffer
	app := &App{
		DB:       db,
		Sessions: sessions,
		Edges:    edges,
		Engine:   query.New(nil, edges, nil),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: "test"},
	}

	err := runSession(context.Background(), app, []string{"end"})
	if err != nil {
		t.Fatalf("session end: %v", err)
	}

	// Session should be marked as processed by reinforcement.
	if !sessions.processed[sessID.String()] {
		t.Error("session should be marked as processed after reinforcement")
	}

	// Output should mention reinforcement (0 edges boosted since stubEdgeRepo returns no edges).
	output := buf.String()
	if !strings.Contains(output, "Reinforced") {
		t.Errorf("expected reinforcement message, got: %s", output)
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

func TestLogSessionEvent_LinkLogsBothEntryIDs(t *testing.T) {
	sessions := newStubSessionRepo()
	sessID := model.NewID()
	sessions.sessions[sessID.String()] = &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
	}

	fromID := model.NewID()
	toID := model.NewID()
	var buf bytes.Buffer
	app := &App{
		Sessions:  sessions,
		SessionID: sessID.String(),
		Printer:   NewPrinter(&buf, false, true),
		Config:    &AppConfig{DefaultScope: "test"},
	}

	logSessionEvent(context.Background(), app, "link", []string{
		fromID.String(), toID.String(), "--type", "related-to",
	})

	if len(sessions.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sessions.events))
	}

	ev := sessions.events[0]
	if ev.EventType != model.EventLink {
		t.Errorf("event type = %s, want link", ev.EventType)
	}
	if len(ev.EntryIDs) != 2 {
		t.Fatalf("entry_ids length = %d, want 2", len(ev.EntryIDs))
	}
	if ev.EntryIDs[0] != fromID || ev.EntryIDs[1] != toID {
		t.Errorf("entry_ids = %v, want [%s, %s]", ev.EntryIDs, fromID, toID)
	}
}

func TestExtractQuery(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"single quoted arg", []string{"test query"}, "test query"},
		{"flag with value before query", []string{"--scope", "foo", "query text"}, "query text"},
		{"empty args", []string{}, ""},
		{"flag with equals", []string{"--scope=foo", "query text"}, "query text"},
		{"double dash separator", []string{"--", "query text"}, "query text"},
		{"only flags", []string{"--scope", "foo"}, ""},
		{"short flag with value", []string{"-s", "foo", "query text"}, "query text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQuery(tt.args)
			if got != tt.want {
				t.Errorf("extractQuery(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
