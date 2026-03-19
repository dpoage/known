package processors

import (
	"strings"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

func init() {
	// Registered as "csv-transform", not "csv" -- the config file uses this name.
	registry.Register("csv-transform", func() registry.Processor {
		return &csvProcessor{}
	})
}

// csvProcessor parses raw CSV data into structured records with named fields.
type csvProcessor struct{}

func (p *csvProcessor) Name() string { return "csv-transform" }

// Validate is a no-op. Real validation happens in pipeline.ValidateChain.
func (p *csvProcessor) Validate() error { return nil }

// Process parses CSV records. The first record is treated as a header row
// that defines field names for subsequent records.
func (p *csvProcessor) Process(records []config.Record) ([]config.Record, error) {
	if len(records) == 0 {
		return records, nil
	}

	// First record is the header.
	headers := parseCSVLine(string(records[0].Raw))

	result := make([]config.Record, 0, len(records)-1)
	for i := 1; i < len(records); i++ {
		rec := records[i]
		values := parseCSVLine(string(rec.Raw))

		fields := make(map[string]string, len(headers))
		for j, h := range headers {
			if j < len(values) {
				fields[h] = values[j]
			} else {
				fields[h] = ""
			}
		}

		rec.Fields = fields
		rec.Meta["source_format"] = "csv"
		rec.Meta["field_count"] = len(fields)
		result = append(result, rec)
	}

	return result, nil
}

// parseCSVLine splits a CSV line respecting quoted fields.
func parseCSVLine(line string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"' && !inQuotes:
			inQuotes = true
		case ch == '"' && inQuotes:
			if i+1 < len(line) && line[i+1] == '"' {
				current.WriteByte('"')
				i++ // skip escaped quote
			} else {
				inQuotes = false
			}
		case ch == ',' && !inQuotes:
			fields = append(fields, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}

	fields = append(fields, strings.TrimSpace(current.String()))
	return fields
}
