package pipeline

import (
	"fmt"
	"log"
	"time"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

// middlewareProc wraps a processor with cross-cutting concerns.
type middlewareProc struct {
	inner registry.Processor
	cfg   *config.Config
}

// WithMiddleware wraps a processor with logging, timing, and recovery
// middleware. The middleware order is: recovery -> timing -> logging -> inner.
func WithMiddleware(proc registry.Processor, cfg *config.Config) registry.Processor {
	return &middlewareProc{inner: proc, cfg: cfg}
}

func (m *middlewareProc) Name() string {
	return m.inner.Name()
}

func (m *middlewareProc) Validate() error {
	return m.inner.Validate()
}

// Process applies middleware in order: recovery wraps timing wraps logging.
func (m *middlewareProc) Process(records []config.Record) (result []config.Record, err error) {
	// Recovery: catch panics and convert to errors.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("processor %s panicked: %v", m.inner.Name(), r)
			result = nil
		}
	}()

	// Timing: measure and log processor duration.
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			log.Printf("SLOW processor %s took %v for %d records",
				m.inner.Name(), elapsed, len(records))
		}
	}()

	// Logging: log input/output counts.
	log.Printf("processor %s: processing %d records", m.inner.Name(), len(records))

	result, err = m.inner.Process(records)
	if err != nil {
		log.Printf("processor %s: error: %v", m.inner.Name(), err)
		return nil, err
	}

	log.Printf("processor %s: produced %d records", m.inner.Name(), len(result))

	// Strict mode validation between steps.
	if m.cfg.Pipeline.StrictMode {
		for _, rec := range result {
			if rec.ID == "" {
				return nil, fmt.Errorf("processor %s: strict mode: record missing ID", m.inner.Name())
			}
		}
	}

	return result, nil
}
