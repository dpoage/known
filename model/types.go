// Package model defines the core data structures for the knowledge graph.
package model

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ID is a ULID-based identifier for all entities.
// ULIDs are sortable, URL-safe, and encode creation time.
type ID struct {
	ulid.ULID
}

// NewID generates a new ID using the current time.
func NewID() ID {
	return ID{ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)}
}

// ParseID parses a string into an ID.
func ParseID(s string) (ID, error) {
	u, err := ulid.Parse(s)
	if err != nil {
		return ID{}, fmt.Errorf("invalid ID %q: %w", s, err)
	}
	return ID{u}, nil
}

// MustParseID parses a string into an ID, panicking on error.
func MustParseID(s string) ID {
	id, err := ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// IsZero returns true if the ID is the zero value.
func (id ID) IsZero() bool {
	return id.ULID.Compare(ulid.ULID{}) == 0
}

// String returns the string representation of the ID.
func (id ID) String() string {
	return id.ULID.String()
}

// MarshalJSON implements json.Marshaler.
func (id ID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.ULID.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (id *ID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Time returns the timestamp encoded in the ULID.
func (id ID) Time() time.Time {
	return ulid.Time(id.ULID.Time())
}

// Metadata provides extensible key-value storage for custom attributes.
type Metadata map[string]any

// Get retrieves a value from metadata, returning nil if not found.
func (m Metadata) Get(key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

// GetString retrieves a string value from metadata.
func (m Metadata) GetString(key string) string {
	v, _ := m.Get(key).(string)
	return v
}

// GetInt retrieves an int value from metadata.
func (m Metadata) GetInt(key string) int {
	switch v := m.Get(key).(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

// Clone creates a deep copy of the metadata.
func (m Metadata) Clone() Metadata {
	if m == nil {
		return nil
	}
	clone := make(Metadata, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}

// SourceType identifies the origin category of knowledge.
type SourceType string

const (
	SourceFile         SourceType = "file"
	SourceURL          SourceType = "url"
	SourceConversation SourceType = "conversation"
	SourceManual       SourceType = "manual"
)

// Source tracks the provenance of a piece of knowledge.
type Source struct {
	Type      SourceType `json:"type"`
	Reference string     `json:"reference"` // path, URL, or identifier
	Meta      Metadata   `json:"meta,omitempty"`
}

// Validate checks that the source has required fields.
func (s Source) Validate() error {
	if s.Type == "" {
		return fmt.Errorf("source type is required")
	}
	if s.Reference == "" {
		return fmt.Errorf("source reference is required")
	}
	return nil
}

// ProvenanceLevel indicates how knowledge was obtained (assertion strength).
// These are stable values, not LLM-generated scores.
type ProvenanceLevel string

const (
	ProvenanceVerified  ProvenanceLevel = "verified"  // user/human stated or confirmed this fact
	ProvenanceInferred  ProvenanceLevel = "inferred"  // agent derived from analysis of source material
	ProvenanceUncertain ProvenanceLevel = "uncertain" // source or conclusion is questionable
)

// Provenance tracks how a piece of knowledge was obtained.
type Provenance struct {
	Level ProvenanceLevel `json:"level"`
}

// Freshness tracks when knowledge was last observed and whether the source has changed.
type Freshness struct {
	ObservedAt time.Time `json:"observed_at"`
	ObservedBy string    `json:"observed_by,omitempty"`
	SourceHash string    `json:"source_hash,omitempty"`
}

// FreshnessLabel returns a display label based on age since last observation:
// "fresh" (< 7d), "aging Nd" (7-30d), "stale Nd" (> 30d).
func (f Freshness) FreshnessLabel() string {
	d := time.Since(f.ObservedAt)
	days := int(d.Hours() / 24)
	switch {
	case days < 7:
		return "fresh"
	case days <= 30:
		return fmt.Sprintf("aging %dd", days)
	default:
		return fmt.Sprintf("stale %dd", days)
	}
}

// ResolutionStatus indicates the state of conflict resolution.
type ResolutionStatus string

const (
	ResolutionUnresolved ResolutionStatus = "unresolved"
	ResolutionResolved   ResolutionStatus = "resolved"
	ResolutionSuperseded ResolutionStatus = "superseded"
)
