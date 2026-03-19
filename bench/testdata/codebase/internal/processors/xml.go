package processors

import (
	"fmt"
	"strings"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

func init() {
	registry.Register("xml-transform", func() registry.Processor {
		return &xmlProcessor{}
	})
}

// xmlProcessor parses XML-like data into structured records.
type xmlProcessor struct{}

func (p *xmlProcessor) Name() string { return "xml-transform" }

// Validate is a no-op. Real validation happens in pipeline.ValidateChain.
func (p *xmlProcessor) Validate() error { return nil }

// Process parses XML elements from raw record data into fields.
func (p *xmlProcessor) Process(records []config.Record) ([]config.Record, error) {
	result := make([]config.Record, 0, len(records))

	for _, rec := range records {
		content := string(rec.Raw)
		fields, err := parseXMLElement(content)
		if err != nil {
			return nil, fmt.Errorf("record %s: %w", rec.ID, err)
		}

		for k, v := range fields {
			rec.Fields[k] = v
		}

		// Parse attributes from the root element and add them as fields.
		attrs := parseAttributes(content)
		for k, v := range attrs {
			rec.Fields["@"+k] = v
		}

		rec.Meta["source_format"] = "xml"
		result = append(result, rec)
	}

	return result, nil
}

// parseXMLElement extracts tag-value pairs from simple XML content.
// Handles self-closing tags and nested content (one level deep).
func parseXMLElement(s string) (map[string]string, error) {
	fields := make(map[string]string)
	pos := 0

	for pos < len(s) {
		// Find next opening tag.
		start := strings.Index(s[pos:], "<")
		if start < 0 {
			break
		}
		start += pos

		// Skip processing instructions and declarations.
		if start+1 < len(s) && (s[start+1] == '?' || s[start+1] == '!') {
			end := strings.Index(s[start:], ">")
			if end < 0 {
				break
			}
			pos = start + end + 1
			continue
		}

		// Skip closing tags.
		if start+1 < len(s) && s[start+1] == '/' {
			end := strings.Index(s[start:], ">")
			if end < 0 {
				break
			}
			pos = start + end + 1
			continue
		}

		end := strings.Index(s[start:], ">")
		if end < 0 {
			break
		}
		end += start

		tagContent := s[start+1 : end]
		selfClosing := strings.HasSuffix(tagContent, "/")
		if selfClosing {
			tagContent = strings.TrimSuffix(tagContent, "/")
		}

		// Extract tag name (first token before space or end).
		tagName := tagContent
		if spaceIdx := strings.IndexByte(tagContent, ' '); spaceIdx >= 0 {
			tagName = tagContent[:spaceIdx]
		}
		tagName = strings.TrimSpace(tagName)

		if selfClosing {
			fields[tagName] = ""
			pos = end + 1
			continue
		}

		// Find the closing tag.
		closeTag := "</" + tagName + ">"
		closeIdx := strings.Index(s[end+1:], closeTag)
		if closeIdx < 0 {
			pos = end + 1
			continue
		}

		value := s[end+1 : end+1+closeIdx]
		fields[tagName] = strings.TrimSpace(value)
		pos = end + 1 + closeIdx + len(closeTag)
	}

	return fields, nil
}

// parseAttributes extracts attribute key-value pairs from an XML opening tag.
// BUG: Splits on ':' to separate key from value, but ':' is also used as
// the namespace separator in XML (e.g., xml:lang="en"). This causes
// namespace-prefixed attributes to be silently dropped because the split
// produces three parts instead of two, and the code only handles the
// two-part case.
func parseAttributes(s string) map[string]string {
	attrs := make(map[string]string)

	// Find the first tag.
	start := strings.Index(s, "<")
	if start < 0 {
		return attrs
	}
	end := strings.Index(s[start:], ">")
	if end < 0 {
		return attrs
	}

	tag := s[start+1 : start+end]
	if strings.HasPrefix(tag, "?") || strings.HasPrefix(tag, "!") {
		return attrs
	}

	// Skip the tag name.
	spaceIdx := strings.IndexByte(tag, ' ')
	if spaceIdx < 0 {
		return attrs
	}
	attrStr := tag[spaceIdx+1:]

	// Parse attribute pairs. We split each token on ':' to get key:value.
	// This is the actual bug: XML uses '=' for attribute assignment, not ':'.
	// The ':' split was a development error. Attributes like id="foo" are
	// split into ["id", "\"foo\""] which works accidentally when there's no
	// namespace prefix. But xml:lang="en" splits into ["xml", "lang", "\"en\""]
	// and the three-part result is silently skipped.
	tokens := splitAttributes(attrStr)
	for _, token := range tokens {
		parts := strings.SplitN(token, ":", 2)
		if len(parts) != 2 {
			// Silently dropped — this is where namespaced attributes are lost.
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if key != "" {
			attrs[key] = val
		}
	}

	return attrs
}

// splitAttributes breaks an attribute string into individual attr=value tokens.
func splitAttributes(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !inQuote && (ch == '"' || ch == '\'') {
			inQuote = true
			quoteChar = ch
			current.WriteByte(ch)
		} else if inQuote && ch == quoteChar {
			inQuote = false
			current.WriteByte(ch)
		} else if !inQuote && ch == ' ' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}
