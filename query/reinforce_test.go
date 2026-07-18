package query

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// mockSessionRepo is an in-memory SessionRepo for testing.
type mockSessionRepo struct {
	sessions  map[string]*model.Session
	events    map[string][]model.SessionEvent
	processed map[string]bool
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{
		sessions:  make(map[string]*model.Session),
		events:    make(map[string][]model.SessionEvent),
		processed: make(map[string]bool),
	}
}

func (m *mockSessionRepo) CreateSession(_ context.Context, session *model.Session) error {
	clone := *session
	m.sessions[session.ID.String()] = &clone
	return nil
}

func (m *mockSessionRepo) EndSession(_ context.Context, id model.ID) error {
	sess, ok := m.sessions[id.String()]
	if !ok {
		return storage.ErrNotFound
	}
	now := time.Now()
	sess.EndedAt = &now
	return nil
}

func (m *mockSessionRepo) GetSession(_ context.Context, id model.ID) (*model.Session, error) {
	sess, ok := m.sessions[id.String()]
	if !ok {
		return nil, storage.ErrNotFound
	}
	clone := *sess
	return &clone, nil
}

func (m *mockSessionRepo) LogEvent(_ context.Context, event *model.SessionEvent) error {
	clone := *event
	key := event.SessionID.String()
	m.events[key] = append(m.events[key], clone)
	return nil
}

func (m *mockSessionRepo) ListEvents(_ context.Context, sessionID model.ID) ([]model.SessionEvent, error) {
	return m.events[sessionID.String()], nil
}

func (m *mockSessionRepo) ListUnprocessedSessions(_ context.Context) ([]model.Session, error) {
	var result []model.Session
	for _, sess := range m.sessions {
		if sess.EndedAt != nil && !m.processed[sess.ID.String()] {
			result = append(result, *sess)
		}
	}
	return result, nil
}

func (m *mockSessionRepo) MarkProcessed(_ context.Context, sessionID model.ID) error {
	m.processed[sessionID.String()] = true
	return nil
}

// noopTx is a pass-through TxFunc for testing (no real transaction needed).
func noopTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func TestReinforce_ActionAfterRecall(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	// Create two entries with an edge.
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("entry one", src).WithScope("test")
	e2 := model.NewEntry("entry two", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	// Create a session with recall → show pattern.
	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID:        sessID,
		StartedAt: now,
		EndedAt:   &ended,
	})

	sessions.LogEvent(ctx, &model.SessionEvent{
		ID:        model.NewID(),
		SessionID: sessID,
		EventType: model.EventRecall,
		Query:     "test query",
		CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID:        model.NewID(),
		SessionID: sessID,
		EventType: model.EventShow,
		EntryIDs:  []model.ID{e1.ID},
		CreatedAt: now.Add(time.Second),
	})

	cfg := DefaultReinforceConfig()
	result, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.SessionsProcessed != 1 {
		t.Errorf("SessionsProcessed = %d, want 1", result.SessionsProcessed)
	}
	if result.EdgesBoosted == 0 {
		t.Error("expected at least 1 edge boosted")
	}

	// Verify edge weight was boosted.
	got, err := edgeRepo.Get(ctx, edge.ID)
	if err != nil {
		t.Fatalf("Get edge: %v", err)
	}
	if got.Weight == nil {
		t.Fatal("edge weight should not be nil after boost")
	}
	// New formula: weight * decay + showBoost = 0.5 * 0.99 + 0.02 = 0.515
	want := 0.5*cfg.DecayFactor + cfg.ShowBoost
	if math.Abs(*got.Weight-want) > 1e-9 {
		t.Errorf("edge weight = %f, want %f", *got.Weight, want)
	}
}

func TestReinforce_NoRecallNoBoost(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	// Create entries and edge.
	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("entry one", src).WithScope("test")
	e2 := model.NewEntry("entry two", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	// Session with show but no preceding recall.
	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID:        sessID,
		StartedAt: now,
		EndedAt:   &ended,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID:        model.NewID(),
		SessionID: sessID,
		EventType: model.EventShow,
		EntryIDs:  []model.ID{e1.ID},
		CreatedAt: now,
	})

	result, err := engine.Reinforce(ctx, sessions, noopTx, DefaultReinforceConfig())
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.EdgesBoosted != 0 {
		t.Errorf("EdgesBoosted = %d, want 0 (no recall preceded show)", result.EdgesBoosted)
	}

	// Edge weight should be unchanged.
	got, _ := edgeRepo.Get(ctx, edge.ID)
	if *got.Weight != 0.5 {
		t.Errorf("edge weight = %f, want 0.5 (unchanged)", *got.Weight)
	}
}

// TestReinforce_WeightCappedAtMax verifies the MaxWeight ceiling is enforced
// even when the boost/decay equilibrium exceeds it. Uses a custom config
// where equilibrium = 0.5/0.1 = 5.0 >> 1.0 to confirm the clamp fires.
func TestReinforce_WeightCappedAtMax(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.9)
	edgeRepo.Create(ctx, &edge)

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID:        sessID,
		StartedAt: now,
		EndedAt:   &ended,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	// Config with equilibrium 0.5/0.1 = 5.0; even from w=0.9 the next step
	// 0.9*0.9+0.5 = 1.31 must be clamped to MaxWeight 1.0.
	cfg := ReinforceConfig{
		ShowBoost:   0.5,
		DecayFactor: 0.9,
		MinWeight:   0.01,
		MaxWeight:   1.0,
	}
	result, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.EdgesBoosted == 0 {
		t.Fatal("expected edges to be boosted")
	}

	got, _ := edgeRepo.Get(ctx, edge.ID)
	if *got.Weight != cfg.MaxWeight {
		t.Errorf("edge weight = %f, want %f (capped at MaxWeight)", *got.Weight, cfg.MaxWeight)
	}
}

func TestReinforce_NilWeightBoosted(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	// Edge with nil weight (defaults to 1.0 effective).
	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo)
	edgeRepo.Create(ctx, &edge)

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID:        sessID,
		StartedAt: now,
		EndedAt:   &ended,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventUpdate,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	result, err := engine.Reinforce(ctx, sessions, noopTx, DefaultReinforceConfig())
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.EdgesBoosted == 0 {
		t.Fatal("expected edges to be boosted")
	}

	cfg := DefaultReinforceConfig()
	// nil weight → EffectiveWeight() = model.DefaultEdgeWeight = 0.5 (neutral).
	// With update action at equilibrium: newWeight = 0.5*0.90 + 0.05 = 0.50.
	// A nil-weight edge at equilibrium stays at equilibrium after one update cycle.
	got, _ := edgeRepo.Get(ctx, edge.ID)
	want := model.DefaultEdgeWeight*cfg.DecayFactor + cfg.UpdateBoost
	if math.Abs(*got.Weight-want) > 1e-9 {
		t.Errorf("edge weight = %f, want %f (DefaultEdgeWeight*decay + updateBoost)", *got.Weight, want)
	}
}

func TestReinforce_OpenSessionIgnored(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	// Open session (not ended) — should be ignored.
	sessID := model.NewID()
	sessions.CreateSession(ctx, &model.Session{
		ID:        sessID,
		StartedAt: time.Now(),
	})

	result, err := engine.Reinforce(ctx, sessions, noopTx, DefaultReinforceConfig())
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.SessionsProcessed != 0 {
		t.Errorf("SessionsProcessed = %d, want 0", result.SessionsProcessed)
	}
}

func TestReinforce_IdempotentProcessing(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID: sessID, StartedAt: now, EndedAt: &ended,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	// First run.
	cfg := DefaultReinforceConfig()
	r1, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce(1): %v", err)
	}
	if r1.SessionsProcessed != 1 {
		t.Fatalf("first run: SessionsProcessed = %d, want 1", r1.SessionsProcessed)
	}

	// Record weight after first run.
	got, _ := edgeRepo.Get(ctx, edge.ID)
	weightAfterFirst := *got.Weight

	// Second run — session already processed.
	r2, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce(2): %v", err)
	}
	if r2.SessionsProcessed != 0 {
		t.Errorf("second run: SessionsProcessed = %d, want 0", r2.SessionsProcessed)
	}

	// Weight should not have changed.
	got, _ = edgeRepo.Get(ctx, edge.ID)
	if *got.Weight != weightAfterFirst {
		t.Errorf("weight changed after idempotent run: %f != %f", *got.Weight, weightAfterFirst)
	}
}

func TestReinforce_SearchAlsoTriggersRecall(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{
		ID: sessID, StartedAt: now, EndedAt: &ended,
	})
	// search (not recall) should also count.
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventSearch,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventShow,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	result, err := engine.Reinforce(ctx, sessions, noopTx, DefaultReinforceConfig())
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.EdgesBoosted == 0 {
		t.Error("expected edges boosted from search→show pattern")
	}
}

func TestReinforce_MultipleSessions(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	e3 := model.NewEntry("e3", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)
	entryRepo.Create(ctx, &e3)

	edge12 := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edge23 := model.NewEdge(e2.ID, e3.ID, model.EdgeRelatedTo).WithWeight(0.7)
	edgeRepo.Create(ctx, &edge12)
	edgeRepo.Create(ctx, &edge23)

	now := time.Now()

	// Session 1: recall → show e1 (should boost edge12).
	s1ID := model.NewID()
	ended1 := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{ID: s1ID, StartedAt: now, EndedAt: &ended1})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: s1ID, EventType: model.EventRecall,
		Query: "q1", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: s1ID, EventType: model.EventShow,
		EntryIDs: []model.ID{e1.ID}, CreatedAt: now.Add(time.Second),
	})

	// Session 2: recall → update e3 (should boost edge23).
	s2ID := model.NewID()
	ended2 := now.Add(2 * time.Minute)
	sessions.CreateSession(ctx, &model.Session{ID: s2ID, StartedAt: now.Add(time.Minute), EndedAt: &ended2})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: s2ID, EventType: model.EventRecall,
		Query: "q2", CreatedAt: now.Add(time.Minute),
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: s2ID, EventType: model.EventUpdate,
		EntryIDs: []model.ID{e3.ID}, CreatedAt: now.Add(time.Minute + time.Second),
	})

	cfg := DefaultReinforceConfig()
	result, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.SessionsProcessed != 2 {
		t.Errorf("SessionsProcessed = %d, want 2", result.SessionsProcessed)
	}
	if result.EdgesBoosted < 2 {
		t.Errorf("EdgesBoosted = %d, want >= 2", result.EdgesBoosted)
	}

	// With default config (decay=0.90): weights above equilibrium decay toward
	// it. edge12 (show, eq=0.20): 0.5*0.90+0.02=0.47. edge23 (update, eq=0.50):
	// 0.7*0.90+0.05=0.68. Both moved; both were touched (EdgesBoosted>=2).
	got12, _ := edgeRepo.Get(ctx, edge12.ID)
	want12 := 0.5*cfg.DecayFactor + cfg.ShowBoost
	if math.Abs(*got12.Weight-want12) > 1e-9 {
		t.Errorf("edge12 weight = %f, want %f (one show cycle)", *got12.Weight, want12)
	}
	got23, _ := edgeRepo.Get(ctx, edge23.ID)
	want23 := 0.7*cfg.DecayFactor + cfg.UpdateBoost
	if math.Abs(*got23.Weight-want23) > 1e-9 {
		t.Errorf("edge23 weight = %f, want %f (one update cycle)", *got23.Weight, want23)
	}
}

func TestReinforce_LinkActionBoostsEdge(t *testing.T) {
	ctx := context.Background()

	entryRepo := newMockEntryRepo()
	edgeRepo := newMockEdgeRepo(entryRepo)
	engine := New(entryRepo, edgeRepo, nil)
	sessions := newMockSessionRepo()

	src := model.Source{Type: model.SourceManual, Reference: "test"}
	e1 := model.NewEntry("e1", src).WithScope("test")
	e2 := model.NewEntry("e2", src).WithScope("test")
	entryRepo.Create(ctx, &e1)
	entryRepo.Create(ctx, &e2)

	edge := model.NewEdge(e1.ID, e2.ID, model.EdgeRelatedTo).WithWeight(0.5)
	edgeRepo.Create(ctx, &edge)

	sessID := model.NewID()
	now := time.Now()
	ended := now.Add(time.Minute)
	sessions.CreateSession(ctx, &model.Session{ID: sessID, StartedAt: now, EndedAt: &ended})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventRecall,
		Query: "q", CreatedAt: now,
	})
	sessions.LogEvent(ctx, &model.SessionEvent{
		ID: model.NewID(), SessionID: sessID, EventType: model.EventLink,
		EntryIDs: []model.ID{e1.ID, e2.ID}, CreatedAt: now.Add(time.Second),
	})

	cfg := DefaultReinforceConfig()
	result, err := engine.Reinforce(ctx, sessions, noopTx, cfg)
	if err != nil {
		t.Fatalf("Reinforce: %v", err)
	}

	if result.EdgesBoosted == 0 {
		t.Error("expected edges boosted from recall→link pattern")
	}

	got, _ := edgeRepo.Get(ctx, edge.ID)
	// New formula: weight * decay + linkBoost = 0.5 * 0.99 + 0.05 = 0.545
	want := 0.5*cfg.DecayFactor + cfg.LinkBoost
	if math.Abs(*got.Weight-want) > 1e-9 {
		t.Errorf("edge weight = %f, want %f", *got.Weight, want)
	}
}

func TestFindActedEntries(t *testing.T) {
	entryID := model.NewID()
	now := time.Now()

	tests := []struct {
		name   string
		events []model.SessionEvent
		want   int
	}{
		{
			name: "recall then show",
			events: []model.SessionEvent{
				{EventType: model.EventRecall, CreatedAt: now},
				{EventType: model.EventShow, EntryIDs: []model.ID{entryID}, CreatedAt: now.Add(time.Second)},
			},
			want: 1,
		},
		{
			name: "show without recall",
			events: []model.SessionEvent{
				{EventType: model.EventShow, EntryIDs: []model.ID{entryID}, CreatedAt: now},
			},
			want: 0,
		},
		{
			name: "recall then add (no entry IDs)",
			events: []model.SessionEvent{
				{EventType: model.EventRecall, CreatedAt: now},
				{EventType: model.EventAdd, CreatedAt: now.Add(time.Second)},
			},
			want: 0,
		},
		{
			name:   "empty events",
			events: nil,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findActedEntries(tt.events)
			if len(got) != tt.want {
				t.Errorf("findActedEntries: got %d entries, want %d", len(got), tt.want)
			}
		})
	}
}
