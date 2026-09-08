package resetstate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func acquireTestLease(t *testing.T, root string) *Lease {
	t.Helper()
	if !Supported() {
		t.Skip("native reset lease not implemented on this platform")
	}
	lease, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	return lease
}

func TestLeaseExclusiveAcrossAliasesAndIndependentInstallations(t *testing.T) {
	root := t.TempDir()
	lease := acquireTestLease(t, root)
	if err := lease.RequireCleanStart(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, alias} {
		other, err := Acquire(path)
		if other != nil || !errors.Is(err, ErrInUse) {
			t.Fatal("same installation acquired twice:", err)
		}
	}
	_ = acquireTestLease(t, t.TempDir())
	lockPath := filepath.Join(root, Directory, lockName)
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Check(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	next := acquireTestLease(t, alias)
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(info, after) {
		t.Fatal("lock inode was removed/replaced:", err)
	}
	if next.Identity() == lease.Identity() {
		t.Fatal("launch identity was reused")
	}
}

func TestLeaseConcurrentAcquisitionHasOneOwner(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unavailable")
	}
	root := t.TempDir()
	var wg sync.WaitGroup
	winners := make(chan *Lease, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			lease, err := Acquire(root)
			if err == nil {
				winners <- lease
			} else {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(winners)
	close(failures)
	count := 0
	for lease := range winners {
		count++
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}
	if count != 1 {
		t.Fatalf("got %d owners", count)
	}
	for err := range failures {
		if !errors.Is(err, ErrInUse) {
			t.Error(err)
		}
	}
}

func TestLeaseRejectsUnsafeMetadataWithoutFollowingIt(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unavailable")
	}
	for _, kind := range []string{"directory_link", "lock_link", "hard_link", "lock_directory", "public_directory", "public_lock", "nonempty_lock"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			state := filepath.Join(root, Directory)
			sentinel := filepath.Join(outside, "retained")
			if err := os.WriteFile(sentinel, []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "directory_link" {
				if err := os.Symlink(outside, state); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(state, 0o700); err != nil {
					t.Fatal(err)
				}
				lock := filepath.Join(state, lockName)
				var err error
				switch kind {
				case "lock_link":
					err = os.Symlink(sentinel, lock)
				case "hard_link":
					err = os.Link(sentinel, lock)
				case "lock_directory":
					err = os.Mkdir(lock, 0o700)
				case "public_directory":
					err = os.Chmod(state, 0o755)
				case "public_lock":
					err = os.WriteFile(lock, nil, 0o644)
				case "nonempty_lock":
					err = os.WriteFile(lock, []byte("unclassified"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			lease, err := Acquire(root)
			if lease != nil || !errors.Is(err, ErrUnsafeState) {
				t.Fatal("accepted unsafe state:", err)
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "retained" {
				t.Fatal("outside sentinel changed:", err)
			}
		})
	}
}

func TestLeaseDetectsReplacedOwnershipDirectory(t *testing.T) {
	root := t.TempDir()
	lease := acquireTestLease(t, root)
	state := filepath.Join(root, Directory)
	if err := os.Rename(state, state+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lease.Check(); !errors.Is(err, ErrUnsafeState) {
		t.Fatal(err)
	}
	if err := lease.Replace(OperationRecord, []byte(`{"version":1}`)); !errors.Is(err, ErrUnsafeState) {
		t.Fatal(err)
	}
}

// This helper is ONLY a fresh test executable against a newly owned temporary
// directory. It imports no production builder, native credentials or listeners.
func TestLeaseProcessHelper(t *testing.T) {
	mode := os.Getenv("ORI_RESET_LEASE_TEST_CHILD")
	if mode == "" {
		return
	}
	root := os.Getenv("ORI_RESET_LEASE_TEST_ROOT")
	lease, err := BeforeStores(root)
	if mode == "blocked" {
		if lease != nil || !errors.Is(err, ErrRecoveryRequired) {
			t.Fatal("constructors not fenced:", err)
		}
		return
	}
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err := lease.Close(); !errors.Is(err, ErrPinned) {
		t.Fatal("process lease released early:", err)
	}
	runtime.GC()
	if duplicate, err := Acquire(root); duplicate != nil || !errors.Is(err, ErrInUse) {
		t.Fatal("ownership lost before process exit:", err)
	}
	// No Store is constructed until the actual host boundary returned.
	if err := os.WriteFile(filepath.Join(root, "constructor-ran"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("leased")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0) // Deliberately no deferred Close: kernel ownership ends on exit.
}

func leaseChild(t *testing.T, root, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	// #nosec G204 -- re-exec only this Go test binary, fixed helper test/arguments.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLeaseProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "ORI_RESET_LEASE_TEST_CHILD=" + mode, "ORI_RESET_LEASE_TEST_ROOT=" + root}
	return cmd
}

func TestLeaseHeldUntilOwnedProcessExits(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unavailable")
	}
	root := t.TempDir()
	cmd := leaseChild(t, root, "hold")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "leased\n" {
		t.Fatal("owned child did not start:", line, err)
	}
	if other, err := Acquire(root); other != nil || !errors.Is(err, ErrInUse) {
		t.Fatal("acquired before child exited:", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	_ = acquireTestLease(t, root)
}

func TestHostFencesConstructorsForAnyRecoveryEvidence(t *testing.T) {
	if !Supported() {
		t.Skip("native lease unavailable")
	}
	for _, name := range []string{"operation.json", "policy.json", ".pending-orphan", "unknown-future-file"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			lease := acquireTestLease(t, root)
			path := filepath.Join(root, Directory, name)
			if err := os.WriteFile(path, []byte(`{"unknown_schema":42}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			output, err := leaseChild(t, root, "blocked").CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			if _, err := os.Stat(filepath.Join(root, "constructor-ran")); !os.IsNotExist(err) {
				t.Fatal("constructor ran before recovery:", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != `{"unknown_schema":42}` {
				t.Fatal("recovery evidence modified:", err)
			}
		})
	}
}
