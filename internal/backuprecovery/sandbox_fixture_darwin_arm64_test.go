//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func sandboxFixtureExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

// Only our compiled test helpers and invented fixture paths reach this seam.
// No raw child output/diagnostics are logged; the caller checks fixed sentinels.
func runSandboxFixture(t *testing.T, profile string, input []byte, helper string) []byte {
	t.Helper()
	if helper != "TestNativeDirectorySandboxHelper" && helper != "TestMetadataBrokerSandboxHelper" && helper != "TestNativeDiskQuerySandboxHelper" {
		t.Fatal("unsupported test helper")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	if err := writer.SetWriteDeadline(time.Now().Add(childDrainLimit)); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { _, err := io.WriteString(writer, profile); _ = writer.Close(); written <- err }()
	timeout := providerReadLimit
	if helper == "TestNativeDiskQuerySandboxHelper" {
		timeout = completeReadLimit
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", "/dev/fd/3", sandboxFixtureExecutable(t), "-test.run=^"+helper+"$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2", "ORI_DISABLE_NATIVE_SECRET_STORE=1", "ORI_BACKUP_DIRECTORY_HELPER=1", "ORI_BACKUP_BROKER_HELPER=1", "ORI_BACKUP_QUERY_HELPER=1"}
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = childDrainLimit
	if !configureReadProcess(cmd) {
		t.Fatal("process ownership unavailable")
	}
	stdout := &cappedReadOutput{limit: 1024, cancel: cancel}
	stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if cmd.Process != nil {
		_ = cmd.Cancel()
	}
	writeErr := <-written
	out, overflow := stdout.result()
	diagnostic, diagnosticOverflow := stderr.result()
	defer clear(diagnostic)
	if err != nil || writeErr != nil || overflow || diagnosticOverflow || len(diagnostic) != 0 {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		t.Fatalf("sandboxed metadata control failed (exit=%d, stderr bytes=%d)", code, len(diagnostic))
	}
	return out
}
