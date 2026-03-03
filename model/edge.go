package model

import (
	"fmt"
	"time"
)

// EdgeType defines the kind of relationship between entries.
type EdgeType string

// Predefined edge types for common relationships.
const (
	EdgeDependsOn   EdgeType = "depends-on"   // X requires Y
	EdgeContradicts EdgeType = "contradicts"  // X conflicts with Y
	EdgeSupersedes  EdgeType = "supersedes"   // X replaces Y (newer version)
	EdgeElaborates  EdgeType = "elaborates"   // X provides detail about Y
	EdgeRelatedTo   EdgeType = "related-to"   // generic association
)

// PredefinedEdgeTypes returns all built-in edge types.
func PredefinedEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeDependsOn,
		EdgeContradicts,
		EdgeSupersedes,
		EdgeElaborates,
		EdgeRelatedTo,
	}
}

// IsPredefined returns true if this is a built-in edge type.
func (et EdgeType) IsPredefined() bool {
	for _, t := range PredefinedEdgeTypes() {
		if et == t {
			return true
		}
	}
	return false
}

// Validate checks if the edge type is valid (non-empty).
func (et EdgeType) Validate() error {
	if et == "" {
		return fmt.Errorf("edge type cannot be empty")
	}
	return nil
}

// Edge represents a directed relationship between two entries.
type Edge struct {
	ID        ID        `json:"id"`
	FromID    ID        `json:"from_id"`
	ToID      ID        `json:"to_id"`
	Type      EdgeType  `json:"type"`
	Weight    *float64  `json:"weight,omitempty"` // optional relationship strength
	Meta      Metadata  `json:"meta,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewEdge creates a new edge with a generated ID and current timestamp.
func NewEdge(from, to ID, edgeType EdgeType) Edge {
	return Edge{
		ID:        NewID(),
		FromID:    from,
		ToID:      to,
		Type:      edgeType,
		CreatedAt: time.Now(),
	}
}

// Validate checks that the edge has all required fields and valid values.
func (e Edge) Validate() error {
	if e.ID.IsZero() {
		return fmt.Errorf("edge ID is required")
	}
	if e.FromID.IsZero() {
		return fmt.Errorf("edge from_id is required")
	}
	if e.ToID.IsZero() {
		return fmt.Errorf("edge to_id is required")
	}
	if e.FromID.ULID == e.ToID.ULID {
		return fmt.Errorf("edge cannot reference itself (from_id == to_id)")
	}
	if err := e.Type.Validate(); err != nil {
		return fmt.Errorf("edge type: %w", err)
	}
	if e.Weight != nil && (*e.Weight < 0 || *e.Weight > 1) {
		return fmt.Errorf("edge weight must be between 0 and 1, got %f", *e.Weight)
	}
	return nil
}

// EffectiveWeight returns the edge's weight, defaulting to 1.0 if unset.
func (e Edge) EffectiveWeight() float64 {
	if e.Weight == nil {
		return 1.0
	}
	return *e.Weight
}

// WithWeight returns a copy of the edge with the specified weight.
func (e Edge) WithWeight(w float64) Edge {
	e.Weight = &w
	return e
}

// WithMeta returns a copy of the edge with the specified metadata.
func (e Edge) WithMeta(m Metadata) Edge {
	e.Meta = m
	return e
}

// Reverse returns a new edge with from and to swapped.
// Useful for bidirectional relationship queries.
func (e Edge) Reverse() Edge {
	return Edge{
		ID:        NewID(),
		FromID:    e.ToID,
		ToID:      e.FromID,
		Type:      e.Type,
		Weight:    e.Weight,
		Meta:      e.Meta.Clone(),
		CreatedAt: time.Now(),
	}
}
