package pipeline

import (
	"fmt"
	"strings"
	"sync"

	"pipeliner/internal/config"
	"pipeliner/internal/registry"
)

// RunParallel executes all processors concurrently on copies of the input
// records, then merges the results. When parallel mode is enabled via config,
// all processors run to completion and errors are collected rather than
// failing fast.
func RunParallel(procs []registry.Processor, records []config.Record) ([]config.Record, error) {
	var (
		mu      sync.Mutex
		errors  []string
		results []config.Record
		wg      sync.WaitGroup
	)

	// Each processor gets its own copy of the input records and appends
	// its output to the shared results slice.
	for _, proc := range procs {
		wg.Add(1)
		go func(p registry.Processor) {
			defer wg.Done()

			input := copyRecords(records)
			output, err := p.Process(input)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: %v", p.Name(), err))
				mu.Unlock()
				return
			}

			// BUG: results slice is appended to without holding the mutex.
			// The mutex only protects the errors slice. Under concurrent
			// execution, this causes a data race on the results slice.
			results = append(results, output...)
		}(proc)
	}

	wg.Wait()

	if len(errors) > 0 {
		return nil, fmt.Errorf("parallel errors:\n  %s", strings.Join(errors, "\n  "))
	}

	return results, nil
}

// copyRecords creates a deep copy of a record slice so processors
// can mutate records without affecting other goroutines.
func copyRecords(records []config.Record) []config.Record {
	cp := make([]config.Record, len(records))
	for i, r := range records {
		cp[i] = config.Record{
			ID:     r.ID,
			Fields: copyMap(r.Fields),
			Meta:   copyMeta(r.Meta),
			Raw:    append([]byte(nil), r.Raw...),
		}
	}
	return cp
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func copyMeta(m map[string]interface{}) map[string]interface{} {
	cp := make(map[string]interface{}, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
