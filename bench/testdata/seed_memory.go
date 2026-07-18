//go:build ignore

// seed_memory.go outputs `known remember` commands that seed the memory database
// with facts a diligent agent would have stored across sessions 1-4 of working
// with the pipeliner codebase.
//
// Usage:
//
//	go run seed_memory.go | sh
package main

import (
	"fmt"
	"strings"
)

func main() {
	facts := []struct {
		text  string
		scope string
	}{
		// Session 1: Onboarding — project structure and entry points
		{
			"The Go module name is 'pipeliner' (declared in go.mod)",
			"pipeliner",
		},
		{
			"The entry point is main.go which calls config.Load, registry.BuildChain, pipeline.ValidateChain, then pipeline.New and Run",
			"pipeliner",
		},
		{
			"Default config path is config/default.yaml; passed as first CLI argument or hardcoded fallback",
			"pipeliner.config",
		},
		{
			"Seven packages under internal/: auth, config, errors, output, pipeline, processors, registry",
			"pipeliner",
		},
		{
			"The Processor interface is defined in registry/registry.go with methods Name(), Process(), and Validate()",
			"pipeliner.registry",
		},
		{
			"config.Record is the data type flowing through the pipeline with fields ID, Fields (map[string]string), Meta (map[string]interface{}), and Raw ([]byte)",
			"pipeliner.config",
		},
		{
			"main.go uses a blank import '_ pipeliner/internal/processors' to trigger init() functions that register processors",
			"pipeliner",
		},
		{
			"registry.BuildChain takes a []string of processor names and returns []Processor by calling registry.Get for each",
			"pipeliner.registry",
		},
		{
			"The registry stores processor factories in a map[string]func() Processor protected by sync.RWMutex",
			"pipeliner.registry",
		},

		// Session 2: Feature Work — registration, pipeline mechanics, config
		{
			"csv.go registers as 'csv-transform' not 'csv' — config must use the registration name",
			"pipeliner.processors",
		},
		{
			"json.go registers as 'json-transform' not 'json'",
			"pipeliner.processors",
		},
		{
			"xml.go registers as 'xml-transform' not 'xml'",
			"pipeliner.processors",
		},
		{
			"filter.go registers as 'filter' — the only processor whose registration name matches the filename",
			"pipeliner.processors",
		},
		{
			"The complete set of registered processor names is: csv-transform, json-transform, xml-transform, filter",
			"pipeliner.registry",
		},
		{
			"default.yaml configures processors: [csv-transform, filter, json-transform] with parallel=false",
			"pipeliner.config",
		},
		{
			"production.yaml sets parallel=true, max_records=500000, strict_mode=true, output.type=webhook, endpoint=https://api.internal.example.com/ingest",
			"pipeliner.config",
		},
		{
			"When pipeline.parallel is false, runSequential is used — it fails fast on the first processor error",
			"pipeliner.pipeline",
		},
		{
			"When pipeline.parallel is true, RunParallel is called — all processors run and errors are collected",
			"pipeliner.pipeline",
		},
		{
			"Sequential mode wraps each processor with WithMiddleware which adds recovery, timing, and logging",
			"pipeliner.pipeline",
		},
		{
			"The filter processor produces format 'passthrough' in formatMap — it preserves whatever format it receives",
			"pipeliner.pipeline",
		},
		{
			"json-transform accepts formats: raw, json, csv. csv-transform accepts: raw, csv. xml-transform accepts: raw, xml",
			"pipeliner.pipeline",
		},
		{
			"output.NewWriter supports two types: 'file' (FileWriter) and 'webhook' (WebhookWriter)",
			"pipeliner.output",
		},
		{
			"The default filter rules: drop records with status='deleted', redact fields named 'password' or 'api_key'",
			"pipeliner.processors",
		},
		{
			"The default processor chain csv-transform -> filter -> json-transform produces final format 'json'",
			"pipeliner.pipeline",
		},

		// Session 3: Bug Investigation — the three planted bugs
		{
			"BUG: config/loader.go Load() calls loadOverrides THEN loadDefaults — defaults overwrite production overrides",
			"pipeliner.config",
		},
		{
			"Because of the config load-order bug, all production.yaml values are silently reverted to default.yaml values",
			"pipeliner.config",
		},
		{
			"After the config bug, effective values are: parallel=false, max_records=10000, output.type=file — production settings are lost",
			"pipeliner.config",
		},
		{
			"BUG: parallel.go RunParallel protects the errors slice with mu.Lock but appends to results WITHOUT holding the mutex — data race",
			"pipeliner.pipeline",
		},
		{
			"The race condition in RunParallel: line 43 does 'results = append(results, output...)' outside any mutex lock",
			"pipeliner.pipeline",
		},
		{
			"BUG: xml.go parseAttributes splits on ':' instead of '=' to separate attribute keys from values",
			"pipeliner.processors",
		},
		{
			"The colon split in parseAttributes causes namespace-prefixed attributes like xml:lang='en' to produce 3 parts and get silently dropped",
			"pipeliner.processors",
		},
		{
			"Simple attributes like id='foo' accidentally work with the colon split because id='foo' has no colon before the value — wait, actually it splits id='foo' which has no colon at all so it also fails. The split should be on '='.",
			"pipeliner.processors",
		},
		{
			"parseAttributes is called from xmlProcessor.Process and stores results as fields prefixed with '@'",
			"pipeliner.processors",
		},

		// Session 4: Refactoring — coupling and blast radius
		{
			"Renaming a processor registration requires updating BOTH the init() call AND the formatMap in pipeline/validate.go",
			"pipeliner.registry",
		},
		{
			"formatMap is a package-level var in internal/pipeline/validate.go that hardcodes processor names and their format compatibility",
			"pipeliner.pipeline",
		},
		{
			"The auth package is only imported by the output package (specifically output/webhook.go) — hidden coupling",
			"pipeliner.auth",
		},
		{
			"Processor.Validate() is a no-op on all current implementations — real validation is in pipeline.ValidateChain",
			"pipeliner.registry",
		},
		{
			"ValidateChain checks: processors exist in formatMap, format compatibility between steps, output format match, and no duplicates",
			"pipeliner.pipeline",
		},
		{
			"If a processor is missing from formatMap, ValidateChain returns 'unknown format mapping' error",
			"pipeliner.pipeline",
		},
		{
			"ValidateChain rejects duplicate processor names in the chain",
			"pipeliner.pipeline",
		},
		{
			"The errors package (internal/errors) is imported only by auth and output — processors and pipeline use plain fmt.Errorf",
			"pipeliner.errors",
		},
		{
			"There are 5 error codes in the errors package: ErrConfig, ErrProcessing, ErrValidation, ErrOutput, ErrAuth",
			"pipeliner.errors",
		},
		{
			"errors.IsCode only works on *PipelineError — returns false for plain errors, creating inconsistent error handling",
			"pipeliner.errors",
		},
		{
			"strict_mode in middleware checks that every output record has a non-empty ID field",
			"pipeliner.pipeline",
		},
		{
			"WebhookWriter uses auth.Validator.SignPayload to create HMAC-SHA256 signatures with timestamps for replay protection",
			"pipeliner.output",
		},
		{
			"RunParallel deep-copies records via copyRecords before handing to each goroutine — but the results merge is unprotected",
			"pipeliner.pipeline",
		},
		{
			"16 .go files exist under internal/: 2 in config, 1 in registry, 4 in pipeline, 4 in processors, 3 in output, 1 in auth, 1 in errors",
			"pipeliner",
		},
		{
			"Pipeline reads input from stdin — each line becomes a Record with ID like rec-0000",
			"pipeliner.pipeline",
		},
		{
			"deriveOverridePath in loader.go converts config/default.yaml to config/production.yaml automatically",
			"pipeliner.config",
		},
	}

	for _, f := range facts {
		// Escape single quotes for shell safety: replace ' with '\''
		escaped := strings.ReplaceAll(f.text, "'", "'\\''")
		fmt.Printf("known remember '%s' --scope %s\n", escaped, f.scope)
	}
}
