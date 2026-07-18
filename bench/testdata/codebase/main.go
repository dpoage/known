package main

import (
	"fmt"
	"os"

	"pipeliner/internal/config"
	"pipeliner/internal/pipeline"
	_ "pipeliner/internal/processors"
	"pipeliner/internal/registry"
)

func main() {
	cfgPath := "config/default.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	procs, err := registry.BuildChain(cfg.Pipeline.Processors)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build processor chain: %v\n", err)
		os.Exit(1)
	}

	if err := pipeline.ValidateChain(procs, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "chain validation failed: %v\n", err)
		os.Exit(1)
	}

	p := pipeline.New(cfg, procs)
	if err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "pipeline failed: %v\n", err)
		os.Exit(1)
	}
}
