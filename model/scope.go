package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// scopePathRegex validates scope path segments.
// Segments must be alphanumeric with optional hyphens/underscores, separated by dots.
var scopePathRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// RootScope is the default top-level scope.
const RootScope = "root"

// Scope represents a hierarchical namespace for organizing knowledge.
// Paths are dot-separated, e.g., "project.auth.oauth".
type Scope struct {
	Path      string    `json:"path"`
	Meta      Metadata  `json:"meta,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewScope creates a new scope with the given path.
func NewScope(path string) Scope {
	return Scope{
		Path:      path,
		CreatedAt: time.Now(),
	}
}

// ParseScopePath splits a scope path into its segments.
func ParseScopePath(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("scope path cannot be empty")
	}

	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if seg == "" {
			return nil, fmt.Errorf("scope path has empty segment at position %d", i)
		}
		if !scopePathRegex.MatchString(seg) {
			return nil, fmt.Errorf("invalid scope segment %q: must be alphanumeric (with hyphens/underscores), starting with a letter", seg)
		}
	}
	return segments, nil
}

// Validate checks that the scope path is properly formatted.
func (s Scope) Validate() error {
	_, err := ParseScopePath(s.Path)
	return err
}

// Segments returns the path split into individual segments.
func (s Scope) Segments() []string {
	segments, _ := ParseScopePath(s.Path)
	return segments
}

// Depth returns the nesting level of the scope (1 for root-level).
func (s Scope) Depth() int {
	return len(s.Segments())
}

// Parent returns the parent scope path, or empty string if at root level.
func (s Scope) Parent() string {
	segments := s.Segments()
	if len(segments) <= 1 {
		return ""
	}
	return strings.Join(segments[:len(segments)-1], ".")
}

// IsParentOf returns true if this scope is a direct parent of the other.
func (s Scope) IsParentOf(other Scope) bool {
	return other.Parent() == s.Path
}

// IsAncestorOf returns true if this scope is an ancestor of the other.
func (s Scope) IsAncestorOf(other Scope) bool {
	if s.Path == other.Path {
		return false
	}
	return strings.HasPrefix(other.Path+".", s.Path+".")
}

// IsDescendantOf returns true if this scope is a descendant of the other.
func (s Scope) IsDescendantOf(other Scope) bool {
	return other.IsAncestorOf(s)
}

// Child returns a new scope path with the given segment appended.
func (s Scope) Child(segment string) (Scope, error) {
	if !scopePathRegex.MatchString(segment) {
		return Scope{}, fmt.Errorf("invalid scope segment %q", segment)
	}
	return Scope{
		Path:      s.Path + "." + segment,
		CreatedAt: time.Now(),
	}, nil
}

// WithMeta returns a copy of the scope with the specified metadata.
func (s Scope) WithMeta(m Metadata) Scope {
	s.Meta = m
	return s
}

// CommonAncestor returns the longest common prefix path between two scopes.
func CommonAncestor(a, b Scope) string {
	segsA := a.Segments()
	segsB := b.Segments()

	var common []string
	for i := 0; i < len(segsA) && i < len(segsB); i++ {
		if segsA[i] != segsB[i] {
			break
		}
		common = append(common, segsA[i])
	}

	if len(common) == 0 {
		return ""
	}
	return strings.Join(common, ".")
}
