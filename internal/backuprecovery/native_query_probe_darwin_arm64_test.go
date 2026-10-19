//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeDiskQuerySandboxHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_QUERY_HELPER") != "1" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxSourcePathBytes+1))
	if err != nil || len(input) > maxSourcePathBytes {
		os.Exit(51)
	}
	directory, err := readNativeDirectory(string(input))
	if err != nil {
		os.Exit(52)
	}
	private := os.Getenv("HOME") // exact private scratch supplied by this test's allowlisted environment
	if !validMetadataPath(private) {
		os.Exit(53)
	}
	ctx, cancel := context.WithTimeout(t.Context(), completeReadLimit)
	defer cancel()
	identity, err := readDiskIdentity(ctx, directory.mount, func(ctx context.Context, device string) (diskMetadata, error) {
		return queryNativeDisk(ctx, device, private)
	})
	if err != nil || !identity.valid() || !directory.unchanged() {
		os.Exit(54)
	}
	if names, err := directory.file.Readdirnames(1); err == nil || len(names) != 0 {
		os.Exit(55)
	}
	if file, err := os.Open(filepath.Join(string(input), "invented-content")); err == nil {
		_ = file.Close()
		os.Exit(56)
	}
	directory.close()
	if _, err := io.WriteString(os.Stdout, "scoped-native-metadata-only\n"); err != nil {
		os.Exit(57)
	}
	os.Exit(0)
}

func TestScopedNativeHelperQuerySandboxProbe(t *testing.T) {
	if os.Getenv("ORI_BACKUP_TEST_NATIVE_QUERY") != "1" {
		t.Skip("requires explicit scoped native-query probe")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "invented-source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "invented-content"), []byte("must not be read"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := scopedOSQuerySandboxProfile(sandboxFixtureExecutable(t), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	out := runSandboxFixture(t, profile, []byte(source), "TestNativeDiskQuerySandboxHelper")
	if string(out) != "scoped-native-metadata-only\n" {
		t.Fatal("contained helper's native query did not complete")
	}
}

// Explicit software-only probe: just this invented source's native backing
// volume/store/parent metadata. No device inventory or external drive actions.
// The test directly owns every sandbox/diskutil child. It is NOT the production
// helper composition (nested pipeline ownership still needs implementation).
func TestScopedNativeQuerySandboxProbe(t *testing.T) {
	if os.Getenv("ORI_BACKUP_TEST_NATIVE_QUERY") != "1" {
		t.Skip("requires explicit scoped native-query probe")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "invented-source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := readNativeDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.close()
	for _, kind := range []systemExecutableKind{systemContainmentLauncher, systemDiskMetadataReader} {
		verified, err := verifySystemExecutable(kind)
		if err != nil {
			t.Fatal(err)
		}
		defer verified.close()
	}
	profile, err := scopedOSQuerySandboxProfile("/usr/sbin/diskutil", []string{source})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), completeReadLimit)
	defer cancel()
	calls := 0
	query := func(parent context.Context, device string) (diskMetadata, error) {
		calls++
		spec, err := nativeDiskReadSpec(device, root)
		if err != nil {
			return diskMetadata{}, errStorageIdentity
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			return diskMetadata{}, errStorageIdentity
		}
		defer func() { _ = reader.Close(); _ = writer.Close() }()
		if writer.SetWriteDeadline(time.Now().Add(childDrainLimit)) != nil {
			return diskMetadata{}, errStorageIdentity
		}
		written := make(chan error, 1)
		go func() { _, err := io.WriteString(writer, profile); _ = writer.Close(); written <- err }()
		childCtx, stop := context.WithTimeout(parent, spec.timeout)
		defer stop()
		arguments := append([]string{"-f", "/dev/fd/3", spec.executable}, spec.arguments...)
		cmd := exec.CommandContext(childCtx, "/usr/bin/sandbox-exec", arguments...)
		cmd.Dir, cmd.Env, cmd.ExtraFiles = root, spec.environment, []*os.File{reader}
		cmd.WaitDelay = childDrainLimit
		if !configureReadProcess(cmd) {
			return diskMetadata{}, errStorageIdentity
		}
		stdout := &cappedReadOutput{limit: spec.stdoutLimit, cancel: stop}
		stderr := &cappedReadOutput{limit: maxReadStderrBytes, cancel: stop}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		err = cmd.Run()
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
		writeErr := <-written
		out, overflow := stdout.result()
		defer clear(out)
		diagnostic, diagnosticOverflow := stderr.result()
		defer clear(diagnostic)
		if err != nil || writeErr != nil || overflow || diagnosticOverflow || len(diagnostic) != 0 {
			code := -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			t.Logf("scoped query unavailable: step=%d, exit=%d, stderr_present=%t, deadline=%t", calls, code, len(diagnostic) != 0, childCtx.Err() != nil)
			return diskMetadata{}, errStorageIdentity
		}
		decoded, decodeErr := decodeDiskMetadata(out, device)
		if decodeErr != nil {
			plist, plistErr := decodeDiskPlist(out)
			t.Logf("scoped metadata decode unavailable: step=%d, plist_valid=%t, error_field=%t, internal_typed=%t, whole_typed=%t, node_matches=%t", calls, plistErr == nil, plist.fields["Error"].kind != "", plist.fields["Internal"].kind == "true" || plist.fields["Internal"].kind == "false", plist.fields["WholeDisk"].kind == "true" || plist.fields["WholeDisk"].kind == "false", plist.fields["DeviceNode"].text == "/dev/"+device)
		} else {
			t.Logf("scoped metadata decoded: step=%d, mounted_known=%t, volume_ids=%t, native_mount_matches=%t, supported_transport=%t, store_count=%d", calls, decoded.mountedKnown, validStorageUUID(decoded.volumeID) && validStorageUUID(decoded.partitionID), decoded.mountPoint == directory.mount.mountPoint, decoded.transport == "Apple Fabric", len(decoded.physicalStores))
		}
		return decoded, decodeErr
	}
	identity, err := readDiskIdentity(ctx, directory.mount, query)
	if err != nil || !identity.valid() || !directory.unchanged() {
		t.Fatal("scoped sandboxed native metadata unavailable")
	}
	t.Logf("software-only native query probe passed: %d exact queries; no repository/password/device actions", calls)
}
