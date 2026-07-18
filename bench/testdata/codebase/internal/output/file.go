package output

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pipeliner/internal/config"
	perrors "pipeliner/internal/errors"
)

// FileWriter writes processed records to a local file.
type FileWriter struct {
	path   string
	format string
}

// NewFileWriter creates a writer that outputs to the given file path.
func NewFileWriter(cfg *config.Config) *FileWriter {
	return &FileWriter{
		path:   cfg.Output.Path,
		format: cfg.Output.Format,
	}
}

// Write serializes records and writes them to the configured file.
func (w *FileWriter) Write(records []config.Record) error {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return perrors.NewOutputError("creating output directory", err)
	}

	f, err := os.Create(w.path)
	if err != nil {
		return perrors.NewOutputError("creating output file", err)
	}
	defer f.Close()

	for _, rec := range records {
		line, err := w.formatRecord(rec)
		if err != nil {
			return perrors.NewOutputError(
				fmt.Sprintf("formatting record %s", rec.ID), err,
			)
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			return perrors.NewOutputError("writing record", err)
		}
	}

	return nil
}

// formatRecord converts a record to the configured output format.
func (w *FileWriter) formatRecord(rec config.Record) (string, error) {
	switch w.format {
	case "json":
		return formatAsJSON(rec), nil
	case "csv":
		return formatAsCSV(rec), nil
	case "xml":
		return formatAsXML(rec), nil
	default:
		// Raw output: use the record's existing bytes.
		return string(rec.Raw), nil
	}
}

func formatAsJSON(rec config.Record) string {
	var b strings.Builder
	b.WriteString(`{"id":"`)
	b.WriteString(rec.ID)
	b.WriteByte('"')
	for k, v := range rec.Fields {
		b.WriteString(`,"`)
		b.WriteString(k)
		b.WriteString(`":"`)
		b.WriteString(v)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func formatAsCSV(rec config.Record) string {
	parts := []string{rec.ID}
	for _, v := range rec.Fields {
		if strings.ContainsAny(v, ",\"\n") {
			v = `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, ",")
}

func formatAsXML(rec config.Record) string {
	var b strings.Builder
	b.WriteString("<record id=\"")
	b.WriteString(rec.ID)
	b.WriteString("\">")
	for k, v := range rec.Fields {
		b.WriteString("<")
		b.WriteString(k)
		b.WriteString(">")
		b.WriteString(escapeXML(v))
		b.WriteString("</")
		b.WriteString(k)
		b.WriteString(">")
	}
	b.WriteString("</record>")
	return b.String()
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
