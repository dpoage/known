package registry

import (
	"fmt"
	"sync"

	"pipeliner/internal/config"
)

// Processor is the interface that all data processors must implement.
type Processor interface {
	// Name returns the registration name of this processor.
	Name() string

	// Process transforms a batch of records and returns the result.
	Process(records []config.Record) ([]config.Record, error)

	// Validate checks whether the processor is properly configured.
	// NOTE: This is a no-op on all current processors. Real validation
	// happens in pipeline.ValidateChain which checks format compatibility
	// across the chain. This method exists for future per-processor checks.
	Validate() error
}

var (
	mu         sync.RWMutex
	processors = make(map[string]func() Processor)
)

// Register adds a processor factory to the global registry.
// Called from init() functions in processor packages.
// The name parameter is the registration name used in config files,
// which may differ from the Go filename.
func Register(name string, factory func() Processor) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := processors[name]; exists {
		panic(fmt.Sprintf("processor %q already registered", name))
	}
	processors[name] = factory
}

// Get returns a new instance of the named processor.
func Get(name string) (Processor, error) {
	mu.RLock()
	defer mu.RUnlock()

	factory, ok := processors[name]
	if !ok {
		return nil, fmt.Errorf("unknown processor %q; registered: %v", name, registeredNames())
	}
	return factory(), nil
}

// BuildChain creates an ordered slice of processors from config names.
func BuildChain(names []string) ([]Processor, error) {
	chain := make([]Processor, 0, len(names))
	for _, name := range names {
		p, err := Get(name)
		if err != nil {
			return nil, fmt.Errorf("building chain: %w", err)
		}
		chain = append(chain, p)
	}
	return chain, nil
}

// ListRegistered returns the names of all registered processors.
func ListRegistered() []string {
	mu.RLock()
	defer mu.RUnlock()
	return registeredNames()
}

func registeredNames() []string {
	names := make([]string, 0, len(processors))
	for name := range processors {
		names = append(names, name)
	}
	return names
}
