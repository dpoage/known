package cmd

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// version is set by goreleaser via ldflags:
//
//	-ldflags "-X github.com/dpoage/known/cmd.version={{.Version}}"
var version = "dev"

// runVersion prints the version string to stdout.
// For release builds (version != "dev"), it prints: known v<version>
// For dev builds, it enriches output with VCS info from debug.ReadBuildInfo.
func runVersion() {
	if version != "dev" {
		fmt.Printf("known v%s\n", version)
		return
	}

	vcs := vcsInfo()
	if vcs == "" {
		fmt.Println("known dev")
		return
	}

	fmt.Printf("known dev (%s)\n", vcs)
}

// vcsInfo extracts VCS revision and dirty flag from build info.
func vcsInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	var revision string
	var dirty bool

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if revision == "" {
		return ""
	}

	// Shorten to 7 characters, matching conventional short SHA.
	if len(revision) > 7 {
		revision = revision[:7]
	}

	var parts []string
	parts = append(parts, revision)
	if dirty {
		parts = append(parts, "dirty")
	}

	return strings.Join(parts, ", ")
}
