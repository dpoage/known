// Package storage defines repository interfaces for the knowledge graph persistence layer.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dpoage/known/model"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = fmt.Errorf("not found")

// ConcurrentModificationError is returned when an update fails due to a version
// mismatch, indicating another writer modified the entry since it was read.
type ConcurrentModificationError struct {
	ID              model.ID
	ExpectedVersion int
	ActualVersion   int
}

func (e *ConcurrentModificationError) Error() string {
	if e.ActualVersion > 0 {
		return fmt.Sprintf("concurrent modification of entry %s: expected version %d, actual version %d",
			e.ID.String(), e.ExpectedVersion, e.ActualVersion)
	}
	return fmt.Sprintf("concurrent modification of entry %s: expected version %d, current version has changed",
		e.ID.String(), e.ExpectedVersion)
}

// IsConcurrentModification returns true if the error is a ConcurrentModificationError.
func IsConcurrentModification(err error) bool {
	var cme *ConcurrentModificationError
	return errors.As(err, &cme)
}

// ErrDuplicateContent is returned when an entry with the same content hash
// and scope already exists.
var ErrDuplicateContent = fmt.Errorf("duplicate content")

// SimilarityMetric specifies the distance function for vector search.
type SimilarityMetric int

const (
	// Cosine computes cosine similarity (1 - cosine distance).
	Cosine SimilarityMetric = iota
	// L2 computes Euclidean distance.
	L2
	// InnerProduct computes the negative inner product.
	InnerProduct
)

// SimilarityResult pairs an entry with its similarity score.
type SimilarityResult struct {
	Entry    model.Entry
	Distance float64 // lower is more similar for L2/cosine distance; interpretation depends on metric
}

// EntryFilter provides filtering criteria for entry queries.
type EntryFilter struct {
	Scope           string                // exact scope match
	ScopePrefix     string                // hierarchical scope match (scope and all descendants)
	SourceType      model.SourceType      // filter by source type (file, url, conversation, manual)
	ProvenanceLevel model.ProvenanceLevel // filter by provenance level
	Labels          []string              // entries must have ALL specified labels (AND semantics)
	StalerThan      time.Duration         // filter entries whose observed_at is older than this duration
	IncludeExpired  bool                  // if false (default), exclude entries past ExpiresAt
	Limit           int
	Offset          int
}

// EdgeFilter provides filtering criteria for edge queries.
type EdgeFilter struct {
	Type  model.EdgeType // filter by edge type
	Limit int
}

// EntryRepo defines persistence operations for knowledge entries.
type EntryRepo interface {
	// Create persists a new entry. Returns an error if the ID already exists.
	// Automatically ensures the scope hierarchy exists.
	Create(ctx context.Context, entry *model.Entry) error

	// CreateOrUpdate inserts a new entry or updates an existing one with the same
	// content hash and scope. This provides idempotent upsert semantics to prevent
	// duplicate knowledge entries from concurrent agents.
	// On insert, the entry is created normally.
	// On conflict (same content_hash + scope), the existing entry is updated.
	// Returns the final entry (with ID and version populated).
	CreateOrUpdate(ctx context.Context, entry *model.Entry) (*model.Entry, error)

	// Get retrieves an entry by ID. Returns ErrNotFound if it does not exist.
	Get(ctx context.Context, id model.ID) (*model.Entry, error)

	// Update replaces an existing entry. Returns ErrNotFound if it does not exist.
	// Uses optimistic concurrency control: the update only succeeds if the entry's
	// version matches the expected version. On version mismatch, returns
	// *ConcurrentModificationError so callers can detect and retry.
	// On success, the entry's Version field is incremented.
	Update(ctx context.Context, entry *model.Entry) error

	// Delete removes an entry by ID. Returns ErrNotFound if it does not exist.
	Delete(ctx context.Context, id model.ID) error

	// List returns entries matching the given filter.
	List(ctx context.Context, filter EntryFilter) ([]model.Entry, error)

	// SearchSimilar finds entries with embeddings similar to the query vector.
	// Results are ordered by ascending distance (most similar first).
	// The dimension parameter must match the embedding dimension of stored entries.
	SearchSimilar(ctx context.Context, query []float32, scope string, metric SimilarityMetric, limit int) ([]SimilarityResult, error)

	// DeleteExpired removes entries whose ExpiresAt is in the past.
	// Returns the number of entries deleted.
	DeleteExpired(ctx context.Context) (int64, error)
}

// EdgeRepo defines persistence operations for graph edges.
type EdgeRepo interface {
	// Create persists a new edge. Returns an error if the ID already exists.
	Create(ctx context.Context, edge *model.Edge) error

	// Get retrieves an edge by ID. Returns ErrNotFound if it does not exist.
	Get(ctx context.Context, id model.ID) (*model.Edge, error)

	// Update modifies an existing edge's weight and metadata.
	// Returns ErrNotFound if the edge does not exist.
	Update(ctx context.Context, edge *model.Edge) error

	// Delete removes an edge by ID. Returns ErrNotFound if it does not exist.
	Delete(ctx context.Context, id model.ID) error

	// EdgesFrom returns all outgoing edges from the given entry.
	EdgesFrom(ctx context.Context, entryID model.ID, filter EdgeFilter) ([]model.Edge, error)

	// EdgesTo returns all incoming edges to the given entry.
	EdgesTo(ctx context.Context, entryID model.ID, filter EdgeFilter) ([]model.Edge, error)

	// EdgesBetween returns edges from source to target (directional).
	EdgesBetween(ctx context.Context, fromID, toID model.ID) ([]model.Edge, error)

	// FindConflicts returns entries that have a "contradicts" edge involving the given entry.
	FindConflicts(ctx context.Context, entryID model.ID) ([]model.Entry, error)
}

// SessionRepo defines persistence operations for session tracking.
type SessionRepo interface {
	// CreateSession persists a new session.
	CreateSession(ctx context.Context, session *model.Session) error

	// EndSession sets the ended_at timestamp on a session.
	EndSession(ctx context.Context, id model.ID) error

	// GetSession retrieves a session by ID. Returns ErrNotFound if it does not exist.
	GetSession(ctx context.Context, id model.ID) (*model.Session, error)

	// LogEvent persists a session event.
	LogEvent(ctx context.Context, event *model.SessionEvent) error

	// ListEvents returns all events for a session, ordered by created_at.
	ListEvents(ctx context.Context, sessionID model.ID) ([]model.SessionEvent, error)

	// ListUnprocessedSessions returns ended sessions not yet in session_reinforcements.
	ListUnprocessedSessions(ctx context.Context) ([]model.Session, error)

	// MarkProcessed records that a session's events have been processed for reinforcement.
	MarkProcessed(ctx context.Context, sessionID model.ID) error
}

// LabelLister can enumerate all distinct labels in the knowledge graph.
type LabelLister interface {
	ListLabels(ctx context.Context) ([]string, error)
}

// Backend is the top-level interface for a storage backend (postgres, sqlite, etc.).
// It provides access to the individual repositories and manages connections/transactions.
type Backend interface {
	Entries() EntryRepo
	Edges() EdgeRepo
	Scopes() ScopeRepo
	Sessions() SessionRepo
	Labels() LabelLister
	WithTx(ctx context.Context, fn func(context.Context) error) error
	Close() error
	Migrate() error
}

// ScopeRepo defines persistence operations for scope metadata.
type ScopeRepo interface {
	// Upsert creates or updates a scope. The path is the natural key.
	Upsert(ctx context.Context, scope *model.Scope) error

	// Get retrieves a scope by path. Returns ErrNotFound if it does not exist.
	Get(ctx context.Context, path string) (*model.Scope, error)

	// Delete removes a scope by path. Returns ErrNotFound if it does not exist.
	Delete(ctx context.Context, path string) error

	// List returns all scopes, ordered by path.
	List(ctx context.Context) ([]model.Scope, error)

	// ListChildren returns direct child scopes of the given path.
	ListChildren(ctx context.Context, parentPath string) ([]model.Scope, error)

	// ListDescendants returns all descendant scopes of the given path.
	ListDescendants(ctx context.Context, ancestorPath string) ([]model.Scope, error)

	// DeleteEmpty removes scopes that have no entries and no descendant entries.
	// Returns the number of scopes deleted.
	DeleteEmpty(ctx context.Context) (int64, error)
}
