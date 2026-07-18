package pipeline

import (
	"fmt"
	"io"
	"os"

	"pipeliner/internal/config"
	"pipeliner/internal/output"
	"pipeliner/internal/registry"
)

// Pipeline orchestrates the execution of a processor chain.
type Pipeline struct {
	cfg   *config.Config
	procs []registry.Processor
}

// New creates a pipeline with the given config and processor chain.
func New(cfg *config.Config, procs []registry.Processor) *Pipeline {
	return &Pipeline{cfg: cfg, procs: procs}
}

// Run reads input, passes records through the processor chain, and writes output.
func (p *Pipeline) Run() error {
	records, err := p.readInput()
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	if p.cfg.Pipeline.MaxRecords > 0 && len(records) > p.cfg.Pipeline.MaxRecords {
		records = records[:p.cfg.Pipeline.MaxRecords]
	}

	// Choose execution strategy based on config.
	// When parallel=true, error semantics change: all processors run and
	// errors are collected rather than failing fast on the first error.
	var processed []config.Record
	if p.cfg.Pipeline.Parallel {
		processed, err = RunParallel(p.procs, records)
	} else {
		processed, err = p.runSequential(records)
	}
	if err != nil {
		return fmt.Errorf("processing: %w", err)
	}

	writer, err := output.NewWriter(p.cfg)
	if err != nil {
		return fmt.Errorf("creating output writer: %w", err)
	}

	if err := writer.Write(processed); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// runSequential passes records through each processor in order.
// Fails fast on the first error encountered.
func (p *Pipeline) runSequential(records []config.Record) ([]config.Record, error) {
	current := records

	for i, proc := range p.procs {
		wrapped := WithMiddleware(proc, p.cfg)
		result, err := wrapped.Process(current)
		if err != nil {
			return nil, fmt.Errorf("processor %d (%s): %w", i, proc.Name(), err)
		}
		current = result
	}

	return current, nil
}

// readInput reads records from stdin or a configured input source.
func (p *Pipeline) readInput() ([]config.Record, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	records := make([]config.Record, 0)
	// Split input into lines, each line becomes a record.
	start := 0
	id := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				records = append(records, config.Record{
					ID:     fmt.Sprintf("rec-%04d", id),
					Fields: make(map[string]string),
					Meta:   make(map[string]interface{}),
					Raw:    line,
				})
				id++
			}
			start = i + 1
		}
	}

	return records, nil
}
