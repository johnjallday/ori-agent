package shortsocket

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// This fixture belongs to Ori's test-runner contract, not the external tools.
// A runner may nest TMPDIR deeply; socket-owning tests must choose a short,
// private directory rather than exceeding macOS's sockaddr_un path limit.
func TestShortSocketUnderLongTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fixture")
	}
	if len(os.TempDir()) < 100 {
		t.Fatal("fixture must be invoked with the runner's deliberately long TMPDIR")
	}
	directory, err := os.MkdirTemp("/tmp", "ori-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	listener, err := net.Listen("unix", filepath.Join(directory, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
