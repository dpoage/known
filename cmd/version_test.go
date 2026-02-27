package cmd

import (
	"strings"
	"testing"
)

func TestRunVersion_Release(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })

	cap := captureStdout(t)
	runVersion()
	got := cap.restore()

	if got != "known v1.2.3\n" {
		t.Errorf("release version: got %q, want %q", got, "known v1.2.3\n")
	}
}

func TestRunVersion_Dev(t *testing.T) {
	old := version
	version = "dev"
	t.Cleanup(func() { version = old })

	cap := captureStdout(t)
	runVersion()
	got := cap.restore()

	if !strings.HasPrefix(got, "known dev") {
		t.Errorf("dev version: got %q, want prefix %q", got, "known dev")
	}
}

func TestVcsInfo(t *testing.T) {
	// vcsInfo reads from runtime/debug.ReadBuildInfo which is available
	// in test binaries. Just verify it doesn't panic.
	_ = vcsInfo()
}
