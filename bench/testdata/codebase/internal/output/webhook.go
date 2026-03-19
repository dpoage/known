package output

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"pipeliner/internal/auth"
	"pipeliner/internal/config"
	perrors "pipeliner/internal/errors"
)

// WebhookWriter sends processed records to a remote HTTP endpoint.
// Uses the auth package to sign payloads — this is the hidden coupling
// between auth and the pipeline output layer.
type WebhookWriter struct {
	endpoint  string
	validator *auth.Validator
	client    *http.Client
	format    string
}

// NewWebhookWriter creates a writer that POSTs records to the configured endpoint.
func NewWebhookWriter(cfg *config.Config) (*WebhookWriter, error) {
	if cfg.Auth.APIKey == "" {
		return nil, perrors.NewAuthError("webhook output requires auth.api_key")
	}

	return &WebhookWriter{
		endpoint:  cfg.Output.Endpoint,
		validator: auth.NewValidator(&cfg.Auth),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		format: cfg.Output.Format,
	}, nil
}

// Write serializes all records and sends them as a single POST request.
func (w *WebhookWriter) Write(records []config.Record) error {
	payload := w.serialize(records)

	sig, ts := w.validator.SignPayload(payload)

	req, err := http.NewRequest(http.MethodPost, w.endpoint, bytes.NewReader(payload))
	if err != nil {
		return perrors.NewOutputError("creating request", err)
	}

	req.Header.Set("Content-Type", w.contentType())
	req.Header.Set("X-Signature", sig)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-API-Key", "present") // marker header for authenticated requests

	resp, err := w.client.Do(req)
	if err != nil {
		return perrors.NewOutputError("sending webhook", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return perrors.NewOutputError(
			fmt.Sprintf("webhook returned %d: %s", resp.StatusCode, string(body)),
			nil,
		)
	}

	return nil
}

func (w *WebhookWriter) contentType() string {
	switch w.format {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	default:
		return "text/plain"
	}
}

func (w *WebhookWriter) serialize(records []config.Record) []byte {
	var b strings.Builder

	switch w.format {
	case "json":
		b.WriteByte('[')
		for i, rec := range records {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(formatAsJSON(rec))
		}
		b.WriteByte(']')
	case "xml":
		b.WriteString("<records>")
		for _, rec := range records {
			b.WriteString(formatAsXML(rec))
		}
		b.WriteString("</records>")
	default:
		for _, rec := range records {
			b.Write(rec.Raw)
			b.WriteByte('\n')
		}
	}

	return []byte(b.String())
}
