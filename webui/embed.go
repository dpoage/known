// Package webui serves the embedded web-based graph explorer for known.
package webui

import "embed"

// Assets holds the embedded frontend files served by the explorer HTTP server.
//
//go:embed all:assets
var Assets embed.FS
