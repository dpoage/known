package processors

import (
	"fmt"
	"strings"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

func init() {
	// Registered as "json-transform", not "json".
	registry.Register("json-transform", func() registry.Processor {
		return &jsonProcessor{}
	})
}

// jsonProcessor converts records to JSON-structured output.
type jsonProcessor struct{}

func (p *jsonProcessor) Name() string { return "json-transform" }

// Validate is a no-op. Real validation happens in pipeline.ValidateChain.
func (p *jsonProcessor) Validate() error { return nil }

// Process converts record fields into a JSON byte representation stored in Raw.
// If fields are empty, it attempts to parse Raw as simple JSON key-value pairs.
func (p *jsonProcessor) Process(records []config.Record) ([]config.Record, error) {
	result := make([]config.Record, 0, len(records))

	for _, rec := range records {
		if len(rec.Fields) > 0 {
			// Convert structured fields to JSON bytes.
			rec.Raw = fieldsToJSON(rec.Fields)
			rec.Meta["source_format"] = "json"
		} else {
			// Try to parse raw bytes as JSON and extract fields.
			fields, err := parseSimpleJSON(string(rec.Raw))
			if err != nil {
				return nil, fmt.Errorf("record %s: %w", rec.ID, err)
			}
			rec.Fields = fields
			rec.Meta["source_format"] = "json"
		}
		result = append(result, rec)
	}

	return result, nil
}

// fieldsToJSON produces a minimal JSON object from a string map.
func fieldsToJSON(fields map[string]string) []byte {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for k, v := range fields {
		if !first {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(escapeJSON(k))
		b.WriteString(`":"`)
		b.WriteString(escapeJSON(v))
		b.WriteByte('"')
		first = false
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// escapeJSON escapes special characters in a JSON string value.
func escapeJSON(s string) string {
	var b strings.Builder
	for _, ch := range s {
		switch ch {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// parseSimpleJSON extracts key-value pairs from a flat JSON object.
// Only handles string values; nested objects are stored as raw strings.
func parseSimpleJSON(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("invalid JSON: expected object")
	}

	s = s[1 : len(s)-1] // strip braces
	fields := make(map[string]string)

	for s != "" {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}

		// Parse key.
		key, rest, err := parseJSONString(s)
		if err != nil {
			return nil, fmt.Errorf("parsing key: %w", err)
		}
		s = strings.TrimSpace(rest)

		if len(s) == 0 || s[0] != ':' {
			return nil, fmt.Errorf("expected ':' after key %q", key)
		}
		s = strings.TrimSpace(s[1:])

		// Parse value.
		val, rest, err := parseJSONString(s)
		if err != nil {
			return nil, fmt.Errorf("parsing value for %q: %w", key, err)
		}

		fields[key] = val
		s = strings.TrimSpace(rest)

		if len(s) > 0 && s[0] == ',' {
			s = s[1:]
		}
	}

	return fields, nil
}

// parseJSONString extracts a quoted string and returns it plus the remaining input.
func parseJSONString(s string) (string, string, error) {
	if len(s) == 0 || s[0] != '"' {
		return "", s, fmt.Errorf("expected '\"'")
	}

	var b strings.Builder
	i := 1
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"', '\\', '/':
				b.WriteByte(s[i+1])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		if s[i] == '"' {
			return b.String(), s[i+1:], nil
		}
		b.WriteByte(s[i])
		i++
	}

	return "", "", fmt.Errorf("unterminated string")
}
