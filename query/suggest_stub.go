// Package query — stub for zv1.3 SuggestLinks interface.
// This file provides the type and method signature so cmd/add.go compiles
// on the zv1.2 branch. Replace with the real implementation in zv1.3.
package query

import (
	"context"

	"github.com/dpoage/known/model"
)

// LinkSuggestion is a candidate link returned by SuggestLinks.
// The real implementation lives in zv1.3 (query/suggest.go).
type LinkSuggestion struct {
	Entry    model.Entry
	Score    float64
	EdgeType model.EdgeType
}

// SuggestLinks returns up to k candidate links for entry based on semantic
// similarity and graph topology. Error is non-fatal; callers should log and
// proceed when suggestions are unavailable.
//
// This stub always returns nil, nil. The real implementation is in zv1.3.
func (e *Engine) SuggestLinks(_ context.Context, _ model.Entry, _ int) ([]LinkSuggestion, error) {
	return nil, nil
}
