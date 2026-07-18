package processors

import (
	"strings"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

func init() {
	registry.Register("filter", func() registry.Processor {
		return &filterProcessor{
			rules: defaultFilterRules(),
		}
	})
}

// filterProcessor removes or transforms records based on configurable rules.
type filterProcessor struct {
	rules []filterRule
}

type filterRule struct {
	Field    string
	Operator string // "contains", "equals", "not_empty", "matches_prefix"
	Value    string
	Action   string // "keep", "drop", "redact"
}

func (p *filterProcessor) Name() string { return "filter" }

// Validate is a no-op. Real validation happens in pipeline.ValidateChain.
func (p *filterProcessor) Validate() error { return nil }

// Process applies filter rules to each record. Records that match a "drop"
// rule are excluded. Records matching "redact" have the matched field cleared.
// Records matching "keep" are always included. If no rules match, the record
// is kept by default.
func (p *filterProcessor) Process(records []config.Record) ([]config.Record, error) {
	result := make([]config.Record, 0, len(records))

	for _, rec := range records {
		action := p.evaluate(rec)
		switch action {
		case "drop":
			continue
		case "redact":
			rec = redactSensitive(rec)
			result = append(result, rec)
		default:
			result = append(result, rec)
		}
	}

	return result, nil
}

// evaluate checks all rules against a record and returns the action.
// First matching rule wins.
func (p *filterProcessor) evaluate(rec config.Record) string {
	for _, rule := range p.rules {
		val, ok := rec.Fields[rule.Field]
		if !ok {
			// Also check raw content for unstructured data.
			val = string(rec.Raw)
		}

		matched := false
		switch rule.Operator {
		case "contains":
			matched = strings.Contains(val, rule.Value)
		case "equals":
			matched = val == rule.Value
		case "not_empty":
			matched = val != ""
		case "matches_prefix":
			matched = strings.HasPrefix(val, rule.Value)
		}

		if matched {
			return rule.Action
		}
	}

	return "keep"
}

// redactSensitive clears fields that look like they contain sensitive data.
func redactSensitive(rec config.Record) config.Record {
	sensitiveKeys := []string{"password", "secret", "token", "api_key", "ssn", "credit_card"}

	for key := range rec.Fields {
		lower := strings.ToLower(key)
		for _, sensitive := range sensitiveKeys {
			if strings.Contains(lower, sensitive) {
				rec.Fields[key] = "[REDACTED]"
				break
			}
		}
	}

	return rec
}

func defaultFilterRules() []filterRule {
	return []filterRule{
		{Field: "status", Operator: "equals", Value: "deleted", Action: "drop"},
		{Field: "password", Operator: "not_empty", Value: "", Action: "redact"},
		{Field: "api_key", Operator: "not_empty", Value: "", Action: "redact"},
	}
}
