//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"
)

type resticReadOperation uint8

const (
	resticReadConfig resticReadOperation = iota + 1
	resticReadSnapshots
)

type providerMetadata struct {
	config    resticConfigMetadata
	snapshots []resticSnapshotMetadata
}

func fixedResticReadArguments(replica string, operation resticReadOperation) ([]string, int, error) {
	if !validMetadataPath(replica) {
		return nil, 0, errReadProcess
	}
	arguments := []string{"--json", "--repo", filepath.Join(replica, "repository"), "--password-file", "/dev/fd/0", "--no-lock", "--no-cache"}
	switch operation {
	case resticReadConfig:
		return append(arguments, "cat", "config"), maxConfigBytes, nil
	case resticReadSnapshots:
		return append(arguments, "snapshots"), maxSnapshotBytes, nil
	default:
		return nil, 0, errReadProcess
	}
}

func providerSandboxProfile(executable string, replica *metadataReplica, sources []string, originalRepository string) (string, error) {
	if replica == nil || !validMetadataPath(originalRepository) || originalRepository == replica.path || pathsOverlap(originalRepository, replica.path) {
		return "", errSandboxProfile
	}
	profile, err := directoryMetadataSandboxProfile(executable, sources)
	if err != nil {
		return "", err
	}
	for _, source := range sources {
		if pathsOverlap(source, replica.path) {
			return "", errSandboxProfile
		}
	}
	repository := filepath.Join(replica.path, "repository")
	profile += "(deny file-read-data (subpath " + strconv.Quote(originalRepository) + "))\n"
	profile += "(allow file-read-data (literal " + strconv.Quote(filepath.Join(repository, "config")) + ") (subpath " + strconv.Quote(filepath.Join(repository, "keys")) + ") (subpath " + strconv.Quote(filepath.Join(repository, "snapshots")) + "))\n"
	if len(profile) > maxSandboxProfileBytes {
		return "", errSandboxProfile
	}
	return profile, nil
}

func pathsOverlap(a, b string) bool {
	return a == b || len(a) > len(b) && a[:len(b)] == b && a[len(b)] == '/' || len(b) > len(a) && b[:len(a)] == a && b[len(a)] == '/'
}

func validProviderPassword(value []byte) bool {
	if len(value) < 1 || len(value) > 4096 || !utf8.Valid(value) {
		return false
	}
	for _, b := range value {
		if b == 0 || b == '\n' || b == '\r' {
			return false
		}
	}
	return true
}

// runResticRead runs only inside the group-leading compiled helper. The provider
// starts with an EMPTY stdin pipe. No credential bytes are released until the
// kernel image/vnode and full-file descriptor pin all match. It can read only a
// private encrypted replica; the original repository is explicitly denied.
func runResticRead(parent context.Context, checked *verifiedExecutable, replica *metadataReplica, sources []string, repository string, password []byte, operation resticReadOperation) ([]byte, error) {
	if parent == nil || parent.Err() != nil || checked == nil || replica == nil || !validProviderPassword(password) {
		return nil, errReadProcess
	}
	arguments, limit, err := fixedResticReadArguments(replica.path, operation)
	if err != nil {
		return nil, err
	}
	if !replica.unchanged() {
		return nil, errMetadataBundle
	}
	if !checked.unchanged(parent) {
		return nil, errTrustedExecutable
	}
	launcher, err := verifySystemExecutable(systemContainmentLauncher)
	if err != nil {
		return nil, errTrustedExecutable
	}
	defer launcher.close()
	profile, err := providerSandboxProfile(checked.path, replica, sources, repository)
	if err != nil {
		return nil, errSandboxProfile
	}
	ctx, cancel := context.WithTimeout(parent, providerReadLimit)
	defer cancel()
	profileReader, profileWriter, err := os.Pipe()
	if err != nil {
		return nil, errReadProcess
	}
	defer func() { _ = profileReader.Close(); _ = profileWriter.Close() }()
	passwordReader, passwordWriter, err := os.Pipe()
	if err != nil {
		return nil, errReadProcess
	}
	defer func() { _ = passwordReader.Close(); _ = passwordWriter.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = passwordWriter.Close(); _ = profileWriter.Close() })
	defer stopClose()
	if profileWriter.SetWriteDeadline(time.Now().Add(childDrainLimit)) != nil {
		return nil, errReadProcess
	}
	profileDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(profileWriter, profile)
		_ = profileWriter.Close()
		profileDone <- err
	}()
	cmdArguments := append([]string{"-f", "/dev/fd/3", checked.path}, arguments...)
	// #nosec G204 -- fixed OS launcher and two compiled operations, separate arguments, private replica only; operator-selected restic is full-digest/mapped-image pinned before any credential transfer. No shell or request-selected command.
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", cmdArguments...)
	cmd.Dir = "/" // fixed loader-readable cwd; no source or private directory enumeration
	cmd.Env = []string{"HOME=" + filepath.Join(replica.path, "home"), "TMPDIR=" + filepath.Join(replica.path, "tmp"), "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"}
	cmd.Stdin = passwordReader
	cmd.ExtraFiles = []*os.File{profileReader}
	cmd.WaitDelay = childDrainLimit
	if !configurePipelineReadProcess(cmd) {
		_ = profileWriter.Close()
		<-profileDone
		return nil, errReadProcess
	}
	stdout := &cappedReadOutput{limit: limit, cancel: cancel}
	stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if cmd.Start() != nil {
		_ = profileWriter.Close()
		<-profileDone
		return nil, errReadProcess
	}
	// Wait must NOT run concurrently with the pre-secret handshake: retaining
	// the unreaped child prevents PID reuse while querying its kernel identity.
	matched := false
	deadline := time.Now().Add(childDrainLimit)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if checkedResticProcess(ctx, cmd.Process, checked) {
			matched = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	transferErr := errReadProcess
	if matched && ctx.Err() == nil {
		end, _ := ctx.Deadline()
		if passwordWriter.SetWriteDeadline(end) == nil {
			payload := append(append(make([]byte, 0, len(password)+1), password...), '\n')
			n, err := passwordWriter.Write(payload)
			if err == nil && n == len(payload) {
				transferErr = nil
			}
			clear(payload)
		}
	}
	_ = passwordWriter.Close()
	if transferErr != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	_ = cmd.Cancel()
	_ = profileWriter.Close()
	profileErr := <-profileDone
	out, overflow := stdout.result()
	diagnostic, diagnosticOverflow := stderr.result()
	defer clear(diagnostic)
	if overflow || diagnosticOverflow {
		clear(out)
		return nil, errReadProcessLimit
	}
	if parent.Err() != nil {
		clear(out)
		return nil, errReadProcessCancelled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		clear(out)
		return nil, errReadProcessDeadline
	}
	if !matched {
		clear(out)
		if bytes.Contains(diagnostic, []byte("sandbox_apply")) || bytes.Contains(diagnostic, []byte("sandbox_init")) || bytes.Contains(diagnostic, []byte("sandbox-exec:")) {
			return nil, errSandboxProfile
		}
		if len(diagnostic) != 0 {
			return nil, errMetadataRead
		}
		return nil, errProviderImage
	}
	if waitErr != nil || transferErr != nil || profileErr != nil || len(diagnostic) != 0 {
		clear(out)
		if transferErr == nil && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 12 {
			return nil, errCredentialUnavailable
		}
		return nil, errMetadataRead
	}
	if !launcher.unchanged() || !checked.unchanged(ctx) {
		clear(out)
		return nil, errTrustedExecutable
	}
	if !replica.unchanged() {
		clear(out)
		return nil, errMetadataBundle
	}
	return out, nil
}

// readResticReplica is the private provider phase, not domain admission. The
// caller must already hold a reviewed read/receipt and the complete pipeline's
// cancellation permit, and must recheck live storage before publishing results.
func readResticReplica(ctx context.Context, executable, scratch string, bundle metadataBundle, sources []string, repository string, password []byte) (result providerMetadata, err error) {
	checked, err := verifyResticExecutable(ctx, executable)
	if err != nil {
		return providerMetadata{}, errTrustedExecutable
	}
	defer checked.close()
	replica, err := stageMetadataReplica(ctx, scratch, bundle)
	if err != nil {
		return providerMetadata{}, errMetadataBundle
	}
	defer func() {
		if replica.close() != nil {
			result = providerMetadata{}
			err = errReadProcess
		}
	}()
	config, err := runResticRead(ctx, checked, replica, sources, repository, password, resticReadConfig)
	if err != nil {
		return providerMetadata{}, err
	}
	defer clear(config)
	result.config, err = decodeResticConfig(config, nil, 0)
	if err != nil {
		return providerMetadata{}, err
	}
	snapshots, err := runResticRead(ctx, checked, replica, sources, repository, password, resticReadSnapshots)
	if err != nil {
		return providerMetadata{}, err
	}
	defer clear(snapshots)
	result.snapshots, err = decodeResticSnapshots(snapshots, nil, 0)
	if err != nil {
		return providerMetadata{}, err
	}
	return result, nil
}
