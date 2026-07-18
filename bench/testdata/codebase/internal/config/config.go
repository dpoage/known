package config

// Config is the top-level configuration for the pipeline.
type Config struct {
	Pipeline PipelineConfig `yaml:"pipeline"`
	Output   OutputConfig   `yaml:"output"`
	Auth     AuthConfig     `yaml:"auth"`
}

// PipelineConfig controls processor chain behavior.
type PipelineConfig struct {
	// Processors lists the registered names of processors to run, in order.
	Processors []string `yaml:"processors"`

	// Parallel enables concurrent processor execution. When true, the
	// error semantics change: instead of fail-fast, all processors run
	// and errors are collected. This is NOT obvious from the field name.
	Parallel bool `yaml:"parallel"`

	// MaxRecords limits the number of records processed per batch.
	MaxRecords int `yaml:"max_records"`

	// StrictMode enables additional data validation between steps.
	StrictMode bool `yaml:"strict_mode"`
}

// OutputConfig controls where processed data is written.
type OutputConfig struct {
	Type     string `yaml:"type"`     // "file" or "webhook"
	Path     string `yaml:"path"`     // file path (for type=file)
	Endpoint string `yaml:"endpoint"` // URL (for type=webhook)
	Format   string `yaml:"format"`   // "json", "csv", or "xml"
}

// AuthConfig holds credentials for webhook output.
type AuthConfig struct {
	APIKey     string   `yaml:"api_key"`
	AllowedIPs []string `yaml:"allowed_ips"`
}

// Record represents a single data record flowing through the pipeline.
type Record struct {
	ID     string
	Fields map[string]string
	Meta   map[string]interface{}
	Raw    []byte
}

// ProcessorInfo describes a registered processor for validation purposes.
type ProcessorInfo struct {
	Name          string
	AcceptsFormat []string
	OutputFormat  string
}
