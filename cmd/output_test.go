package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

// makeSuggestion builds a LinkSuggestion for an entry with the given scope,
// used to exercise PrintAddResult's cross-scope display without a real
// engine or storage backend.
func makeSuggestion(id model.ID, scope, title string, score float64) query.LinkSuggestion {
	e := model.Entry{ID: id, Scope: scope, Title: title}
	return query.LinkSuggestion{Entry: e, Score: score, EdgeType: model.EdgeRelatedTo}
}

// TestPrintAddResult_CrossScopeSuggestion_Human verifies that the human
// "Link?" line shows a "[<scope>]" suffix only for a suggestion whose scope
// differs from the source entry's scope (known-lxj).
func TestPrintAddResult_CrossScopeSuggestion_Human(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, false, false)

	source := model.NewEntry("renderer architecture decision", model.Source{Type: model.SourceManual})
	source.Scope = "projectA"

	sameScope := makeSuggestion(model.NewID(), "projectA", "same-scope neighbor", 0.9)
	crossScope := makeSuggestion(model.NewID(), "projectB", "cross-scope neighbor", 0.8)

	p.PrintAddResult(source, false, []query.LinkSuggestion{sameScope, crossScope})

	out := buf.String()
	lines := strings.Split(out, "\n")

	var sameLine, crossLine string
	for _, l := range lines {
		if strings.Contains(l, "same-scope neighbor") {
			sameLine = l
		}
		if strings.Contains(l, "cross-scope neighbor") {
			crossLine = l
		}
	}
	if sameLine == "" || crossLine == "" {
		t.Fatalf("expected both suggestion lines in output, got:\n%s", out)
	}

	if strings.Contains(sameLine, "[projectA]") || strings.Contains(sameLine, "["+sameScope.Entry.Scope+"]") {
		t.Errorf("same-scope suggestion line should not show a scope suffix, got: %q", sameLine)
	}
	if !strings.Contains(crossLine, "[projectB]") {
		t.Errorf("cross-scope suggestion line should show its scope, got: %q", crossLine)
	}
}

// TestPrintAddResult_JSON_SuggestionScope verifies the JSON suggestion
// schema carries a "scope" field (additive, backward-compatible) reflecting
// the suggestion entry's own scope regardless of whether it matches the
// source entry's scope.
func TestPrintAddResult_JSON_SuggestionScope(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, true, false)

	source := model.NewEntry("renderer architecture decision", model.Source{Type: model.SourceManual})
	source.Scope = "projectA"

	crossScope := makeSuggestion(model.NewID(), "projectB", "cross-scope neighbor", 0.8)
	p.PrintAddResult(source, false, []query.LinkSuggestion{crossScope})

	var decoded addResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON output: %v\noutput: %s", err, buf.String())
	}
	if len(decoded.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(decoded.Suggestions))
	}
	if decoded.Suggestions[0].Scope != "projectB" {
		t.Errorf("expected suggestion scope %q, got %q", "projectB", decoded.Suggestions[0].Scope)
	}
	// Existing fields must remain present and correctly populated
	// (backward-compatible additive field).
	if decoded.Suggestions[0].ID != crossScope.Entry.ID.String() {
		t.Errorf("expected suggestion ID %q, got %q", crossScope.Entry.ID, decoded.Suggestions[0].ID)
	}
	if decoded.Suggestions[0].Title != "cross-scope neighbor" {
		t.Errorf("expected suggestion title %q, got %q", "cross-scope neighbor", decoded.Suggestions[0].Title)
	}
}
