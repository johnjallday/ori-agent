//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMappedExecutableRejectsDifferentVnode(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openExecutableNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := statAnchor(file)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = self.Release() }()
	if !mappedExecutableMatches(self, info) {
		t.Fatal("current executable vnode was not bound")
	}
	info.Ino++
	if mappedExecutableMatches(self, info) {
		t.Fatal("different executable vnode was accepted")
	}
}

// Explicit opt-in software probe. Normal CI needs neither restic nor a drive.
// This executes only version on the digest-verified staged binary, under the
// write/network-denying observer profile. No repository/password is involved.
func TestStagedResticKernelProcessIdentity(t *testing.T) {
	path := os.Getenv("ORI_BACKUP_TEST_RESTIC")
	if path == "" {
		t.Skip("requires an explicitly staged, pinned test executable")
	}
	checked, err := verifyResticExecutable(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer checked.close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`(version 1)
(deny default)
(deny network*)
(allow process-fork)
(allow sysctl-read)
(allow file-read-metadata)
(allow file-read-data (literal "/"))
(allow process-exec (literal %s))
(allow file-read* (literal %s))
(allow file-read* (subpath "/System/Library") (subpath "/usr/lib")
 (subpath "/private/var/db/timezone") (literal "/private/etc/localtime")
 (literal "/private/etc/passwd") (literal "/dev/null")
 (literal "/dev/urandom") (literal "/dev/random") (subpath "/dev/fd"))`, strconv.Quote(path), strconv.Quote(path))
	ctx, cancel := context.WithTimeout(t.Context(), providerReadLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, path, "version")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"}
	cmd.WaitDelay = childDrainLimit
	if !configureReadProcess(cmd) {
		t.Fatal("no bounded process ownership")
	}
	stdout := &cappedReadOutput{limit: 1024, cancel: cancel}
	stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		t.Fatal("sandboxed process unavailable")
	}
	// Do not reap while querying: its PID cannot be reused for another process
	// before Wait. Production credential transfer must use the same ordering.
	matched := false
	deadline := time.Now().Add(childDrainLimit)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if pinnedResticProcess(cmd.Process) && mappedExecutableMatches(cmd.Process, checked.before) {
			matched = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !matched {
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	_ = cmd.Cancel()
	out, overflow := stdout.result()
	diagnostic, diagnosticOverflow := stderr.result()
	defer clear(diagnostic)
	if !matched || err != nil || overflow || diagnosticOverflow || len(diagnostic) != 0 || len(out) == 0 || !checked.unchanged(t.Context()) {
		t.Fatal("pinned process identity probe failed")
	}
}
