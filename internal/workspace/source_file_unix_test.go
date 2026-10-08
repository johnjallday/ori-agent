//go:build !windows

package workspace

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe waits for a writer when it is opened for reading. It must be
// refused without being opened, so a read can never hang on one.
func TestReadContainedFile_RefusesAPipeWithoutWaitingOnIt(t *testing.T) {
	root, _ := containedFixture(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Skip("named pipes are unavailable here")
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadContainedFile(root, "pipe", 1024)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSourceNotRegular) {
			t.Fatalf("a pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a pipe did not return")
	}
}
