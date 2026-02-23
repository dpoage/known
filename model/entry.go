package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// MaxContentLength is the maximum allowed length (in bytes) for entry content.
// Entries exceeding this limit should be broken into smaller pieces.
const MaxContentLength = 4096

// MaxTitleLength is the maximum allowed length (in bytes) for entry titles.
const MaxTitleLength = 200

// ComputeContentHash returns the SHA-256 hex digest of the given content string.
func ComputeContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Entry represents a single piece of knowledge in the graph.
type Entry struct {
	ID             ID         `json:"id"`
	Title          string     `json:"title,omitempty"`
	Content        string     `json:"content"`
	ContentHash    string     `json:"content_hash,omitempty"` // SHA-256 hex digest of content
	Embedding      []float32  `json:"embedding,omitempty"`
	EmbeddingDim   int        `json:"embedding_dim,omitempty"`   // dimension for mixed model support
	EmbeddingModel string     `json:"embedding_model,omitempty"` // which model generated this
	Source         Source     `json:"source"`
	Provenance     Provenance `json:"provenance"`
	Freshness      Freshness  `json:"freshness"`
	Scope          string     `json:"scope"` // hierarchical scope path
	TTL            *Duration  `json:"ttl,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Meta           Metadata   `json:"meta,omitempty"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Conflict tracking - populated by queries, not stored directly
	ConflictsWith    []ID             `json:"-"`
	ResolutionStatus ResolutionStatus `json:"-"`
}

// Duration wraps time.Duration for JSON serialization.
type Duration struct {
	time.Duration
}

// MarshalJSON implements json.Marshaler for Duration.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.Duration.String() + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler for Duration.
func (d *Duration) UnmarshalJSON(data []byte) error {
	// Remove quotes
	if len(data) < 2 {
		return fmt.Errorf("invalid duration")
	}
	s := string(data[1 : len(data)-1])
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// NewEntry creates a new entry with a generated ID, timestamps, and content hash.
func NewEntry(content string, source Source) Entry {
	now := time.Now()
	return Entry{
		ID:          NewID(),
		Content:     content,
		ContentHash: ComputeContentHash(content),
		Source:      source,
		Scope:       RootScope,
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
		Provenance: Provenance{
			Level: ProvenanceInferred,
		},
		Freshness: Freshness{
			ObservedAt: now,
		},
	}
}

// Validate checks that the entry has all required fields and valid values.
func (e Entry) Validate() error {
	if e.ID.IsZero() {
		return fmt.Errorf("entry ID is required")
	}
	if len(e.Title) > MaxTitleLength {
		return fmt.Errorf("title exceeds maximum length (%d > %d)", len(e.Title), MaxTitleLength)
	}
	if e.Content == "" {
		return fmt.Errorf("entry content is required")
	}
	if len(e.Content) > MaxContentLength {
		return fmt.Errorf("content exceeds maximum length (%d > %d); break into smaller entries", len(e.Content), MaxContentLength)
	}
	if err := e.Source.Validate(); err != nil {
		return fmt.Errorf("entry source: %w", err)
	}
	if e.Scope == "" {
		return fmt.Errorf("entry scope is required")
	}
	if _, err := ParseScopePath(e.Scope); err != nil {
		return fmt.Errorf("entry scope: %w", err)
	}
	if len(e.Embedding) > 0 && e.EmbeddingDim != len(e.Embedding) {
		return fmt.Errorf("embedding dimension mismatch: declared %d, actual %d", e.EmbeddingDim, len(e.Embedding))
	}
	return nil
}

// IsExpired returns true if the entry has passed its expiration time.
func (e Entry) IsExpired() bool {
	if e.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*e.ExpiresAt)
}

// SetTTL sets the TTL and calculates the expiration time.
func (e *Entry) SetTTL(ttl time.Duration) {
	e.TTL = &Duration{ttl}
	expiresAt := e.CreatedAt.Add(ttl)
	e.ExpiresAt = &expiresAt
}

// WithEmbedding returns a copy of the entry with the embedding set.
func (e Entry) WithEmbedding(embedding []float32, model string) Entry {
	e.Embedding = embedding
	e.EmbeddingDim = len(embedding)
	e.EmbeddingModel = model
	return e
}

// WithTitle returns a copy of the entry with the title set.
func (e Entry) WithTitle(title string) Entry {
	e.Title = title
	return e
}

// WithScope returns a copy of the entry with the scope set.
func (e Entry) WithScope(scope string) Entry {
	e.Scope = scope
	return e
}

// WithProvenance returns a copy of the entry with the provenance set.
func (e Entry) WithProvenance(p Provenance) Entry {
	e.Provenance = p
	return e
}

// WithFreshness returns a copy of the entry with the freshness set.
func (e Entry) WithFreshness(f Freshness) Entry {
	e.Freshness = f
	return e
}

// WithMeta returns a copy of the entry with the metadata set.
func (e Entry) WithMeta(m Metadata) Entry {
	e.Meta = m
	return e
}

// Touch updates the UpdatedAt timestamp to now and recomputes the content hash.
func (e *Entry) Touch() {
	e.UpdatedAt = time.Now()
	e.ContentHash = ComputeContentHash(e.Content)
}

// HasEmbedding returns true if the entry has an embedding vector.
func (e Entry) HasEmbedding() bool {
	return len(e.Embedding) > 0
}

// HasConflicts returns true if conflicts have been detected.
func (e Entry) HasConflicts() bool {
	return len(e.ConflictsWith) > 0
}

// ScopeObj returns the Scope object for this entry's scope path.
func (e Entry) ScopeObj() Scope {
	return Scope{Path: e.Scope}
}
