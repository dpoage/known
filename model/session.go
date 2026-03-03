package model

import "time"

// EventType identifies the kind of CLI action that occurred.
type EventType string

const (
	EventRecall  EventType = "recall"
	EventSearch  EventType = "search"
	EventShow    EventType = "show"
	EventAdd     EventType = "add"
	EventUpdate  EventType = "update"
	EventLink    EventType = "link"
	EventDelete  EventType = "delete"
)

// Session represents an agent interaction session.
type Session struct {
	ID        ID         `json:"id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Scope     string     `json:"scope,omitempty"`
	Agent     string     `json:"agent,omitempty"`
}

// SessionEvent records a single CLI action within a session.
type SessionEvent struct {
	ID        ID        `json:"id"`
	SessionID ID        `json:"session_id"`
	EventType EventType `json:"event_type"`
	EntryIDs  []ID      `json:"entry_ids,omitempty"`
	EdgeIDs   []ID      `json:"edge_ids,omitempty"`
	Query     string    `json:"query,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
