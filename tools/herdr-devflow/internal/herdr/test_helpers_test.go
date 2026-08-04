package herdr

import (
	"os"
	"testing"
)

// shortSocketDir returns an owned, auto-cleaned temporary directory short
// enough to hold a Unix-domain socket within sockaddr_un's path limit. The
// repository's isolated test sandbox (scripts/run-test-command.sh) points
// TMPDIR at a per-run directory nested under the OS temp root, which on
// macOS is frequently too long by the time a test appends its own file name.
// Darwin exposes a short /private/tmp alias; other platforms fall back to
// the normal process temp directory, which is not similarly constrained.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	parent := os.TempDir()
	if _, err := os.Stat("/private/tmp"); err == nil {
		parent = "/private/tmp"
	}
	directory, err := os.MkdirTemp(parent, "herdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
