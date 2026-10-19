//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

type ownBackupHelper struct {
	executable *verifiedExecutable
	code       [20]byte
	arguments  []string // compiled fixture prefix only; production uses no prefix
}

// The helper is THIS application image, not PATH, a workspace setting, an HTTP
// executable override or a separately installed companion. Its current mapped
// vnode/signing identity must match the complete descriptor we retain.
func verifyOwnBackupHelper(ctx context.Context) (*ownBackupHelper, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errTrustedExecutable
	}
	path, err := os.Executable()
	if err != nil {
		return nil, errTrustedExecutable
	}
	file, err := openExecutableNoFollow(path)
	if err != nil {
		return nil, errTrustedExecutable
	}
	defer func() { _ = file.Close() }()
	info, err := statAnchor(file)
	var fs unix.Statfs_t
	if err != nil || !trustedExecutableStatWithin(info, maxApplicationHelperBytes) || unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Flags&unix.MNT_LOCAL == 0 {
		return nil, errTrustedExecutable
	}
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		return nil, errTrustedExecutable
	}
	defer func() { _ = self.Release() }()
	code, valid := processCodeIdentity(self)
	if !valid || !mappedExecutableMatches(self, info) {
		return nil, errTrustedExecutable
	}
	hash := sha256.New()
	reader := io.NewSectionReader(file, 0, info.Size)
	buffer := make([]byte, 32<<10)
	for ctx.Err() == nil {
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errTrustedExecutable
		}
	}
	if ctx.Err() != nil {
		return nil, errTrustedExecutable
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	checked, err := verifyExecutableWithin(ctx, path, digest, maxApplicationHelperBytes)
	if err != nil {
		return nil, errTrustedExecutable
	}
	if !mappedExecutableMatches(self, checked.before) {
		checked.close()
		return nil, errTrustedExecutable
	}
	return &ownBackupHelper{executable: checked, code: code}, nil
}

func (helper *ownBackupHelper) close() {
	if helper != nil {
		helper.executable.close()
	}
}
func (helper *ownBackupHelper) matches(ctx context.Context, process *os.Process) bool {
	if helper == nil || helper.executable == nil {
		return false
	}
	code, valid := processCodeIdentity(process)
	if !valid || code != helper.code || !mappedExecutableMatches(process, helper.executable.before) || !helper.executable.unchanged(ctx) {
		return false
	}
	code, valid = processCodeIdentity(process)
	return valid && code == helper.code
}

// run owns one group-leading child until it is reaped. For the reader, the host
// checks its own exact helper image BEFORE invoking the scoped Vault callback;
// that helper in turn pins restic before forwarding any password bytes.
func (helper *ownBackupHelper) run(parent context.Context, mode, profile, scratch string, request []byte, usePassword backupPasswordUse) ([]byte, error) {
	if parent == nil || parent.Err() != nil || helper == nil || helper.executable == nil || !validMetadataPath(scratch) || len(profile) > maxSandboxProfileBytes ||
		(mode != "broker-v1" && mode != "reader-v1" && mode != "timemachine-v1") || (mode == "reader-v1") != (usePassword != nil) || len(request) > maxHelperInputBytes-4096 {
		return nil, errReadProcess
	}
	timeout, limit := 75*time.Second, maxBrokerOutputBytes
	if mode == "reader-v1" || mode == "timemachine-v1" {
		timeout, limit = 45*time.Second, maxPrivateStateBytes
	}
	if !helper.executable.unchanged(parent) {
		return nil, errTrustedExecutable
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	input, writer, err := os.Pipe()
	if err != nil {
		return nil, errReadProcess
	}
	defer func() { _ = input.Close(); _ = writer.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = writer.Close() })
	defer stop()
	arguments := append(append([]string(nil), helper.arguments...), backupHelperPrefix+mode)
	program := helper.executable.path
	var profileReader, profileWriter *os.File
	finishProfile := func() error { return nil }
	if profile != "" {
		launcher, err := verifySystemExecutable(systemContainmentLauncher)
		if err != nil {
			return nil, errSandboxProfile
		}
		defer launcher.close()
		profileReader, profileWriter, err = os.Pipe()
		if err != nil {
			return nil, errReadProcess
		}
		defer func() { _ = profileReader.Close(); _ = profileWriter.Close() }()
		if profileWriter.SetWriteDeadline(time.Now().Add(childDrainLimit)) != nil {
			return nil, errReadProcess
		}
		done := make(chan error, 1)
		go func() { _, err := io.WriteString(profileWriter, profile); _ = profileWriter.Close(); done <- err }()
		finishProfile = func() error { _ = profileWriter.Close(); return <-done }
		program = "/usr/bin/sandbox-exec"
		arguments = append([]string{"-f", "/dev/fd/3", helper.executable.path}, arguments...)
	}
	// #nosec G204 -- only this descriptor/mapped-image-pinned application and its two compiled private modes, optionally under the fixed OS launcher. No request-selected executable/operation.
	cmd := exec.CommandContext(ctx, program, arguments...)
	cmd.Dir = "/"
	cmd.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2", "ORI_DISABLE_NATIVE_SECRET_STORE=1"}
	cmd.Stdin = input
	if profileReader != nil {
		cmd.ExtraFiles = []*os.File{profileReader}
	}
	cmd.WaitDelay = childDrainLimit
	if !configureReadProcess(cmd) {
		_ = finishProfile()
		return nil, errReadProcess
	}
	stdout := &cappedReadOutput{limit: limit, cancel: cancel}
	stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if cmd.Start() != nil {
		_ = finishProfile()
		return nil, errReadProcess
	}
	matched := false
	deadline := time.Now().Add(childDrainLimit)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if helper.matches(ctx, cmd.Process) {
			matched = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	transferOK := false
	credentialFailed := false
	if matched && ctx.Err() == nil {
		end, _ := ctx.Deadline()
		if writer.SetWriteDeadline(end) == nil {
			n, err := writer.Write(request)
			transferOK = err == nil && n == len(request)
			if transferOK && usePassword != nil {
				err = usePassword(ctx, func(value []byte) error {
					if !validProviderPassword(value) || ctx.Err() != nil {
						return errReadProcess
					}
					n, err := writer.Write(value)
					if err != nil || n != len(value) {
						return errReadProcess
					}
					return nil
				})
				transferOK = err == nil
				credentialFailed = err != nil
			}
		}
	}
	_ = writer.Close()
	if !transferOK {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	_ = cmd.Cancel()
	profileErr := finishProfile()
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
	if ctx.Err() != nil {
		clear(out)
		return nil, errReadProcessDeadline
	}
	if !matched {
		clear(out)
		return nil, errProviderImage
	}
	if credentialFailed {
		clear(out)
		return nil, errCredentialUnavailable
	}
	if waitErr != nil || !transferOK || profileErr != nil || len(diagnostic) != 0 {
		clear(out)
		if waitErr != nil && cmd.ProcessState != nil {
			return nil, helperFailureError(cmd.ProcessState.ExitCode())
		}
		return nil, errMetadataRead
	}
	if !helper.executable.unchanged(ctx) {
		clear(out)
		return nil, errTrustedExecutable
	}
	return out, nil
}
