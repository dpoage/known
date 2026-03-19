package pipeline

import (
	"fmt"
	"strings"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

// formatMap records which formats each processor accepts and produces.
// This is the actual validation logic. The Processor.Validate() method
// on individual processors is a no-op; chain-level format compatibility
// is what matters.
var formatMap = map[string]struct {
	Accepts  []string
	Produces string
}{
	"csv-transform":  {Accepts: []string{"raw", "csv"}, Produces: "csv"},
	"json-transform": {Accepts: []string{"raw", "json", "csv"}, Produces: "json"},
	"xml-transform":  {Accepts: []string{"raw", "xml"}, Produces: "xml"},
	"filter":         {Accepts: []string{"raw", "csv", "json", "xml"}, Produces: "passthrough"},
}

// ValidateChain checks that the processor chain is valid: processors exist,
// format compatibility is maintained, and output format matches the configured
// output format.
func ValidateChain(procs []registry.Processor, cfg *config.Config) error {
	if len(procs) == 0 {
		return fmt.Errorf("empty processor chain")
	}

	currentFormat := "raw"

	for i, proc := range procs {
		info, ok := formatMap[proc.Name()]
		if !ok {
			return fmt.Errorf("processor %d (%s): unknown format mapping", i, proc.Name())
		}

		if !acceptsFormat(info.Accepts, currentFormat) {
			return fmt.Errorf(
				"processor %d (%s): cannot accept format %q; accepts %v",
				i, proc.Name(), currentFormat, info.Accepts,
			)
		}

		if info.Produces == "passthrough" {
			// Filter preserves the current format.
			continue
		}
		currentFormat = info.Produces
	}

	// Validate that the final format is compatible with the output format.
	if cfg.Output.Format != "" && currentFormat != cfg.Output.Format {
		return fmt.Errorf(
			"chain produces %q but output expects %q",
			currentFormat, cfg.Output.Format,
		)
	}

	// Check for duplicate processors which usually indicates a config error.
	seen := make(map[string]bool)
	for _, proc := range procs {
		if seen[proc.Name()] {
			return fmt.Errorf("duplicate processor %q in chain", proc.Name())
		}
		seen[proc.Name()] = true
	}

	return nil
}

// acceptsFormat checks if a format is in the accepted list.
func acceptsFormat(accepted []string, format string) bool {
	for _, a := range accepted {
		if strings.EqualFold(a, format) {
			return true
		}
	}
	return false
}
