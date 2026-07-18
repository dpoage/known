# Pipeliner

A configurable data pipeline tool for ETL workflows.

## Overview

Pipeliner reads data from stdin, passes it through a chain of configurable
processors, and writes the result to a file or webhook endpoint.

## Architecture

- **Processors** are registered by name in `internal/registry` using `init()` functions.
  Config files reference processors by their registration name (e.g., `csv-transform`).
- **Pipeline** executes processors sequentially or in parallel depending on config.
  Parallel mode collects all errors instead of failing fast.
- **Middleware** wraps each processor with logging, timing, and panic recovery.
- **Output** writes to local files or remote webhook endpoints.
- **Auth** handles API key validation and HMAC signing for webhook output.

## Configuration

Place YAML config files in `config/`. The loader reads a base config and
merges in a production override if present.

```yaml
pipeline:
  processors: [csv-transform, filter, json-transform]
  parallel: false
  max_records: 10000

output:
  type: file
  path: output/results.json
  format: json
```

## Usage

```bash
cat input.csv | go run main.go
cat input.csv | go run main.go config/production.yaml
```

## Processors

| Name | File | Description |
|------|------|-------------|
| csv-transform | processors/csv.go | Parse CSV into structured records |
| json-transform | processors/json.go | Convert records to JSON format |
| xml-transform | processors/xml.go | Parse XML elements and attributes |
| filter | processors/filter.go | Filter and redact records by rules |
