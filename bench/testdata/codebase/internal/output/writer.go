package output

import (
	"fmt"

	"pipeliner/internal/config"
)

// Writer is the interface for all output destinations.
type Writer interface {
	Write(records []config.Record) error
}

// NewWriter creates the appropriate output writer based on config.
func NewWriter(cfg *config.Config) (Writer, error) {
	switch cfg.Output.Type {
	case "file":
		return NewFileWriter(cfg), nil
	case "webhook":
		return NewWebhookWriter(cfg)
	default:
		return nil, fmt.Errorf("unknown output type: %q", cfg.Output.Type)
	}
}
