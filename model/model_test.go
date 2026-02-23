package model

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// ID Tests
// =============================================================================

func TestNewID(t *testing.T) {
	id1 := NewID()
	id2 := NewID()

	if id1.IsZero() {
		t.Error("NewID() returned zero ID")
	}
	if id1.ULID == id2.ULID {
		t.Error("NewID() returned duplicate IDs")
	}
}

func TestParseID(t *testing.T) {
	original := NewID()
	parsed, err := ParseID(original.String())
	if err != nil {
		t.Fatalf("ParseID() error: %v", err)
	}
	if parsed.ULID != original.ULID {
		t.Errorf("ParseID() = %v, want %v", parsed, original)
	}
}

func TestParseID_Invalid(t *testing.T) {
	_, err := ParseID("invalid")
	if err == nil {
		t.Error("ParseID(invalid) should return error")
	}
}

func TestID_IsZero(t *testing.T) {
	var zero ID
	if !zero.IsZero() {
		t.Error("zero ID.IsZero() should be true")
	}

	nonZero := NewID()
	if nonZero.IsZero() {
		t.Error("non-zero ID.IsZero() should be false")
	}
}

func TestID_JSON(t *testing.T) {
	original := NewID()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var parsed ID
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if parsed.ULID != original.ULID {
		t.Errorf("JSON round-trip failed: got %v, want %v", parsed, original)
	}
}

func TestID_Time(t *testing.T) {
	before := time.Now().Add(-time.Second)
	id := NewID()
	after := time.Now().Add(time.Second)

	idTime := id.Time()
	if idTime.Before(before) || idTime.After(after) {
		t.Errorf("ID.Time() = %v, want between %v and %v", idTime, before, after)
	}
}

// =============================================================================
// Metadata Tests
// =============================================================================

func TestMetadata_Get(t *testing.T) {
	m := Metadata{"key": "value", "num": 42}

	if m.Get("key") != "value" {
		t.Error("Get(key) should return value")
	}
	if m.Get("missing") != nil {
		t.Error("Get(missing) should return nil")
	}
}

func TestMetadata_GetString(t *testing.T) {
	m := Metadata{"key": "value", "num": 42}

	if m.GetString("key") != "value" {
		t.Error("GetString(key) should return value")
	}
	if m.GetString("num") != "" {
		t.Error("GetString(num) should return empty for non-string")
	}
}

func TestMetadata_GetInt(t *testing.T) {
	m := Metadata{"int": 42, "float": 3.14, "str": "hello"}

	if m.GetInt("int") != 42 {
		t.Error("GetInt(int) should return 42")
	}
	if m.GetInt("float") != 3 {
		t.Error("GetInt(float) should return 3")
	}
	if m.GetInt("str") != 0 {
		t.Error("GetInt(str) should return 0 for non-numeric")
	}
}

func TestMetadata_Clone(t *testing.T) {
	original := Metadata{"key": "value"}
	clone := original.Clone()

	clone["key"] = "modified"
	if original["key"] != "value" {
		t.Error("Clone() should create independent copy")
	}
}

func TestMetadata_Clone_Nil(t *testing.T) {
	var m Metadata
	if m.Clone() != nil {
		t.Error("Clone() of nil should return nil")
	}
}

// =============================================================================
// Source Tests
// =============================================================================

func TestSource_Validate(t *testing.T) {
	tests := []struct {
		name    string
		source  Source
		wantErr bool
	}{
		{
			name:    "valid file source",
			source:  Source{Type: SourceFile, Reference: "/path/to/file.go"},
			wantErr: false,
		},
		{
			name:    "valid url source",
			source:  Source{Type: SourceURL, Reference: "https://example.com"},
			wantErr: false,
		},
		{
			name:    "missing type",
			source:  Source{Reference: "/path/to/file.go"},
			wantErr: true,
		},
		{
			name:    "missing reference",
			source:  Source{Type: SourceFile},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// EdgeType Tests
// =============================================================================

func TestEdgeType_IsPredefined(t *testing.T) {
	if !EdgeDependsOn.IsPredefined() {
		t.Error("EdgeDependsOn should be predefined")
	}
	if !EdgeContradicts.IsPredefined() {
		t.Error("EdgeContradicts should be predefined")
	}
	if EdgeType("custom").IsPredefined() {
		t.Error("custom edge type should not be predefined")
	}
}

func TestEdgeType_Validate(t *testing.T) {
	if err := EdgeDependsOn.Validate(); err != nil {
		t.Errorf("EdgeDependsOn.Validate() error: %v", err)
	}
	if err := EdgeType("").Validate(); err == nil {
		t.Error("empty EdgeType.Validate() should return error")
	}
}

// =============================================================================
// Edge Tests
// =============================================================================

func TestNewEdge(t *testing.T) {
	from := NewID()
	to := NewID()
	edge := NewEdge(from, to, EdgeDependsOn)

	if edge.ID.IsZero() {
		t.Error("NewEdge() should generate ID")
	}
	if edge.FromID != from {
		t.Error("NewEdge() should set FromID")
	}
	if edge.ToID != to {
		t.Error("NewEdge() should set ToID")
	}
	if edge.Type != EdgeDependsOn {
		t.Error("NewEdge() should set Type")
	}
}

func TestEdge_Validate(t *testing.T) {
	from := NewID()
	to := NewID()

	tests := []struct {
		name    string
		edge    Edge
		wantErr bool
	}{
		{
			name:    "valid edge",
			edge:    NewEdge(from, to, EdgeDependsOn),
			wantErr: false,
		},
		{
			name:    "missing ID",
			edge:    Edge{FromID: from, ToID: to, Type: EdgeDependsOn},
			wantErr: true,
		},
		{
			name:    "missing FromID",
			edge:    Edge{ID: NewID(), ToID: to, Type: EdgeDependsOn},
			wantErr: true,
		},
		{
			name:    "self-reference",
			edge:    Edge{ID: NewID(), FromID: from, ToID: from, Type: EdgeDependsOn},
			wantErr: true,
		},
		{
			name:    "empty type",
			edge:    Edge{ID: NewID(), FromID: from, ToID: to, Type: ""},
			wantErr: true,
		},
		{
			name:    "custom type valid",
			edge:    Edge{ID: NewID(), FromID: from, ToID: to, Type: "custom-type"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.edge.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEdge_Weight(t *testing.T) {
	edge := NewEdge(NewID(), NewID(), EdgeRelatedTo)

	// Valid weight
	weighted := edge.WithWeight(0.5)
	if weighted.Weight == nil || *weighted.Weight != 0.5 {
		t.Error("WithWeight() should set weight")
	}
	if err := weighted.Validate(); err != nil {
		t.Errorf("valid weight should pass validation: %v", err)
	}

	// Invalid weight (out of range)
	invalidWeight := edge.WithWeight(1.5)
	if err := invalidWeight.Validate(); err == nil {
		t.Error("weight > 1 should fail validation")
	}

	negativeWeight := edge.WithWeight(-0.1)
	if err := negativeWeight.Validate(); err == nil {
		t.Error("negative weight should fail validation")
	}
}

func TestEdge_Reverse(t *testing.T) {
	from := NewID()
	to := NewID()
	edge := NewEdge(from, to, EdgeDependsOn).WithWeight(0.8)

	reversed := edge.Reverse()

	if reversed.FromID != to || reversed.ToID != from {
		t.Error("Reverse() should swap FromID and ToID")
	}
	if reversed.Type != EdgeDependsOn {
		t.Error("Reverse() should preserve Type")
	}
	if reversed.Weight == nil || *reversed.Weight != 0.8 {
		t.Error("Reverse() should preserve Weight")
	}
	if reversed.ID == edge.ID {
		t.Error("Reverse() should generate new ID")
	}
}

// =============================================================================
// Scope Tests
// =============================================================================

func TestIsValidScopeSegment(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"root", true},
		{"my-project", true},
		{"auth_v2", true},
		{"Project123", true},
		{"a", true},
		{".hidden", false},
		{"123numeric", false},
		{"-dashed", false},
		{"_under", false},
		{"", false},
		{"has space", false},
		{"has.dot", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsValidScopeSegment(tt.input); got != tt.want {
				t.Errorf("IsValidScopeSegment(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseScopePath(t *testing.T) {
	tests := []struct {
		path    string
		want    []string
		wantErr bool
	}{
		{"root", []string{"root"}, false},
		{"project", []string{"project"}, false},
		{"project.auth", []string{"project", "auth"}, false},
		{"project.auth.oauth", []string{"project", "auth", "oauth"}, false},
		{"my-project", []string{"my-project"}, false},
		{"my_project", []string{"my_project"}, false},
		{"Project123", []string{"Project123"}, false},
		{"", nil, true},
		{".leading", nil, true},
		{"trailing.", nil, true},
		{"double..dot", nil, true},
		{"123invalid", nil, true},
		{"-invalid", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := ParseScopePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseScopePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !equalStrings(got, tt.want) {
				t.Errorf("ParseScopePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestScope_Parent(t *testing.T) {
	tests := []struct {
		path   string
		parent string
	}{
		{"root", ""},
		{"project", ""},
		{"project.auth", "project"},
		{"project.auth.oauth", "project.auth"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			s := NewScope(tt.path)
			if got := s.Parent(); got != tt.parent {
				t.Errorf("Parent() = %q, want %q", got, tt.parent)
			}
		})
	}
}

func TestScope_Relationships(t *testing.T) {
	project := NewScope("project")
	auth := NewScope("project.auth")
	oauth := NewScope("project.auth.oauth")
	other := NewScope("other")

	// IsParentOf
	if !project.IsParentOf(auth) {
		t.Error("project should be parent of project.auth")
	}
	if project.IsParentOf(oauth) {
		t.Error("project should NOT be direct parent of project.auth.oauth")
	}

	// IsAncestorOf
	if !project.IsAncestorOf(auth) {
		t.Error("project should be ancestor of project.auth")
	}
	if !project.IsAncestorOf(oauth) {
		t.Error("project should be ancestor of project.auth.oauth")
	}
	if project.IsAncestorOf(other) {
		t.Error("project should NOT be ancestor of other")
	}

	// IsDescendantOf
	if !oauth.IsDescendantOf(project) {
		t.Error("oauth should be descendant of project")
	}
}

func TestScope_Child(t *testing.T) {
	parent := NewScope("project")
	child, err := parent.Child("auth")
	if err != nil {
		t.Fatalf("Child() error: %v", err)
	}
	if child.Path != "project.auth" {
		t.Errorf("Child() = %q, want %q", child.Path, "project.auth")
	}

	// Invalid segment
	_, err = parent.Child("123invalid")
	if err == nil {
		t.Error("Child() with invalid segment should return error")
	}
}

func TestCommonAncestor(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"project.auth.oauth", "project.auth.jwt", "project.auth"},
		{"project.auth", "project.api", "project"},
		{"project", "other", ""},
		{"project.auth.oauth", "project.auth.oauth", "project.auth.oauth"},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := CommonAncestor(NewScope(tt.a), NewScope(tt.b))
			if got != tt.want {
				t.Errorf("CommonAncestor() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Entry Tests
// =============================================================================

func TestNewEntry(t *testing.T) {
	source := Source{Type: SourceFile, Reference: "/test.go"}
	entry := NewEntry("test content", source)

	if entry.ID.IsZero() {
		t.Error("NewEntry() should generate ID")
	}
	if entry.Content != "test content" {
		t.Error("NewEntry() should set Content")
	}
	if entry.Scope != RootScope {
		t.Errorf("NewEntry() scope = %q, want %q", entry.Scope, RootScope)
	}
	if entry.Provenance.Level != ProvenanceInferred {
		t.Error("NewEntry() should default to inferred provenance")
	}
}

func TestEntry_Validate(t *testing.T) {
	validSource := Source{Type: SourceFile, Reference: "/test.go"}
	validEntry := NewEntry("test content", validSource)

	tests := []struct {
		name    string
		modify  func(*Entry)
		wantErr bool
	}{
		{
			name:    "valid entry",
			modify:  func(e *Entry) {},
			wantErr: false,
		},
		{
			name:    "missing ID",
			modify:  func(e *Entry) { e.ID = ID{} },
			wantErr: true,
		},
		{
			name:    "missing content",
			modify:  func(e *Entry) { e.Content = "" },
			wantErr: true,
		},
		{
			name:    "content at max length",
			modify:  func(e *Entry) { e.Content = string(make([]byte, MaxContentLength)) },
			wantErr: false,
		},
		{
			name:    "content exceeds max length",
			modify:  func(e *Entry) { e.Content = string(make([]byte, MaxContentLength+1)) },
			wantErr: true,
		},
		{
			name:    "missing source type",
			modify:  func(e *Entry) { e.Source.Type = "" },
			wantErr: true,
		},
		{
			name:    "missing scope",
			modify:  func(e *Entry) { e.Scope = "" },
			wantErr: true,
		},
		{
			name:    "invalid scope",
			modify:  func(e *Entry) { e.Scope = "123invalid" },
			wantErr: true,
		},
		{
			name: "embedding dimension mismatch",
			modify: func(e *Entry) {
				e.Embedding = []float32{1, 2, 3}
				e.EmbeddingDim = 5
			},
			wantErr: true,
		},
		{
			name: "valid embedding",
			modify: func(e *Entry) {
				e.Embedding = []float32{1, 2, 3}
				e.EmbeddingDim = 3
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := validEntry // copy
			tt.modify(&entry)
			err := entry.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEntry_TTL(t *testing.T) {
	source := Source{Type: SourceFile, Reference: "/test.go"}
	entry := NewEntry("test content", source)

	if entry.IsExpired() {
		t.Error("entry without TTL should not be expired")
	}

	// Set TTL in the past
	entry.SetTTL(-time.Hour)
	if !entry.IsExpired() {
		t.Error("entry with past TTL should be expired")
	}

	// Set TTL in the future
	entry.SetTTL(time.Hour)
	if entry.IsExpired() {
		t.Error("entry with future TTL should not be expired")
	}
}

func TestEntry_WithEmbedding(t *testing.T) {
	source := Source{Type: SourceFile, Reference: "/test.go"}
	entry := NewEntry("test content", source)

	embedding := []float32{0.1, 0.2, 0.3}
	withEmb := entry.WithEmbedding(embedding, "test-model")

	if !withEmb.HasEmbedding() {
		t.Error("WithEmbedding() should set embedding")
	}
	if withEmb.EmbeddingDim != 3 {
		t.Errorf("EmbeddingDim = %d, want 3", withEmb.EmbeddingDim)
	}
	if withEmb.EmbeddingModel != "test-model" {
		t.Errorf("EmbeddingModel = %q, want %q", withEmb.EmbeddingModel, "test-model")
	}

	// Original should be unchanged
	if entry.HasEmbedding() {
		t.Error("original entry should not have embedding")
	}
}

func TestEntry_JSON(t *testing.T) {
	source := Source{Type: SourceFile, Reference: "/test.go", Meta: Metadata{"line": 42}}
	entry := NewEntry("test content", source).
		WithScope("project.auth").
		WithEmbedding([]float32{0.1, 0.2}, "model").
		WithMeta(Metadata{"key": "value"})
	entry.SetTTL(time.Hour)

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var parsed Entry
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if parsed.ID != entry.ID {
		t.Error("JSON round-trip: ID mismatch")
	}
	if parsed.Content != entry.Content {
		t.Error("JSON round-trip: Content mismatch")
	}
	if parsed.Scope != entry.Scope {
		t.Error("JSON round-trip: Scope mismatch")
	}
	if parsed.EmbeddingDim != entry.EmbeddingDim {
		t.Error("JSON round-trip: EmbeddingDim mismatch")
	}
	if parsed.Source.Meta.GetInt("line") != 42 {
		t.Error("JSON round-trip: Source.Meta mismatch")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
