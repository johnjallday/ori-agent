package wakeinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
)

const maxInstalledArtifactBytes = 128 * 1024 * 1024

var ErrInstallationUncertain = errors.New("wake installation self-test is uncertain")

// RootPaths are fixed in production and injectable only through the direct
// test API. No root command-line flag or IPC request can override them.
type RootPaths struct {
	Executable string
	Plist      string
	Socket     string
	StateDir   string
}

// RootConfig is the testable core of the typed root installer.
type RootConfig struct {
	Paths                RootPaths
	SourcePath           string
	AllowedUID           int
	ExpectedDigest       string
	BuildVersion         string
	Now                  func() time.Time
	EUID                 func() int
	PlatformSupported    func() bool
	Chown                func(string, int, int) error
	Launchctl            func(context.Context, string, []string) ([]byte, error)
	SelfTest             func(context.Context, string, string, int, func() time.Time) (time.Time, error)
	HealthCheck          func(context.Context, string, string, int, func() time.Time) (wakeprotocol.Health, error)
	RequireRootOwnership bool
}

// InstallDefault is the only production root-install entry point.
func InstallDefault(
	ctx context.Context,
	allowedUID int,
	expectedDigest string,
	requestedBuild string,
	compiledBuild string,
) error {
	if requestedBuild != compiledBuild {
		return fmt.Errorf("requested build does not match the staged daemon build")
	}
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve staged wake executable: %w", err)
	}
	return InstallRoot(ctx, RootConfig{
		Paths: RootPaths{
			Executable: wakeservice.ExecutablePath,
			Plist:      wakeservice.PlistPath,
			Socket:     wakeservice.SocketPath,
			StateDir:   wakeservice.StateDir,
		},
		SourcePath:           source,
		AllowedUID:           allowedUID,
		ExpectedDigest:       expectedDigest,
		BuildVersion:         compiledBuild,
		RequireRootOwnership: true,
	})
}

// UninstallDefault removes only the fixed Herdr service after the daemon
// proves that no candidate or programmed wake remains.
func UninstallDefault(ctx context.Context, allowedUID int) error {
	if !wakeservice.PlatformSupported() {
		return wakeservice.ErrUnsupported
	}
	if os.Geteuid() != 0 {
		return wakeservice.ErrRequiresRoot
	}
	if allowedUID <= 0 {
		return fmt.Errorf("allowed uid must identify a non-root local user")
	}
	metadataPath := filepath.Join(wakeservice.StateDir, wakeservice.InstallMetadataFile)
	metadata, err := wakeservice.LoadInstallMetadata(metadataPath)
	if err != nil {
		return fmt.Errorf("load installed wake identity: %w", err)
	}
	if metadata.AllowedUID != allowedUID {
		return fmt.Errorf("allowed uid does not match the installed wake service")
	}
	response, err := socketRequest(ctx, wakeservice.SocketPath, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "uninstall-list",
		HelperBuild:     metadata.DaemonBuild,
		Operation:       wakeprotocol.OperationList,
	})
	if err != nil {
		return fmt.Errorf("refuse uninstall without daemon state proof: %w", err)
	}
	if response.Result != wakeprotocol.ResultSuccess || response.State == nil {
		return fmt.Errorf("refuse uninstall because daemon state could not be listed")
	}
	if len(response.State.Candidates) != 0 || response.State.Programmed != nil {
		return fmt.Errorf("refuse uninstall while a Herdr wake candidate is active")
	}
	_, _ = execLaunchctl(ctx, LaunchctlPath, []string{
		"bootout", "system/" + wakeservice.LaunchDaemonLabel,
	})
	if err := removeSafeSocket(wakeservice.SocketPath, allowedUID); err != nil {
		return err
	}
	for _, path := range []string{
		filepath.Join(wakeservice.StateDir, wakeservice.AuditFile),
		filepath.Join(wakeservice.StateDir, wakeservice.StateFile),
		filepath.Join(wakeservice.StateDir, wakeservice.LockFile),
		metadataPath,
	} {
		if err := removeRootRegular(path); err != nil {
			return err
		}
	}
	if err := os.Remove(wakeservice.StateDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove empty wake state directory: %w", err)
	}
	if err := removeRootRegular(wakeservice.PlistPath); err != nil {
		return err
	}
	if err := removeRootRegular(wakeservice.ExecutablePath); err != nil {
		return err
	}
	return nil
}

// InstallRoot verifies the staged artifact, atomically installs fixed files,
// bootstraps launchd, and requires a schedule/read-back/exact-cancel self-test.
func InstallRoot(ctx context.Context, config RootConfig) (returnErr error) {
	defaultRootConfig(&config)
	if !config.PlatformSupported() {
		return wakeservice.ErrUnsupported
	}
	if config.EUID() != 0 {
		return wakeservice.ErrRequiresRoot
	}
	if config.AllowedUID <= 0 {
		return fmt.Errorf("allowed uid must identify a non-root local user")
	}
	if err := validateBuild(config.BuildVersion); err != nil {
		return err
	}
	if len(config.ExpectedDigest) != sha256.Size*2 {
		return fmt.Errorf("expected artifact digest must be a SHA-256 hex digest")
	}
	artifact, digest, err := readApprovedArtifact(config.SourcePath)
	if err != nil {
		return err
	}
	if digest != config.ExpectedDigest {
		return fmt.Errorf("staged artifact digest does not match the approved preview")
	}

	if err := ensureRootDirectory(config.Paths.StateDir, config); err != nil {
		return err
	}
	executableBefore, err := captureFile(config.Paths.Executable, maxInstalledArtifactBytes)
	if err != nil {
		return err
	}
	plistBefore, err := captureFile(config.Paths.Plist, 64*1024)
	if err != nil {
		return err
	}
	metadataPath := filepath.Join(config.Paths.StateDir, wakeservice.InstallMetadataFile)
	metadataBefore, err := captureFile(metadataPath, 64*1024)
	if err != nil {
		return err
	}
	changed := false
	defer func() {
		if returnErr == nil || errors.Is(returnErr, ErrInstallationUncertain) || !changed {
			return
		}
		_, _ = config.Launchctl(ctx, LaunchctlPath, []string{
			"bootout", "system/" + wakeservice.LaunchDaemonLabel,
		})
		_ = restoreCaptured(config.Paths.Executable, executableBefore, config)
		_ = restoreCaptured(config.Paths.Plist, plistBefore, config)
		_ = restoreCaptured(metadataPath, metadataBefore, config)
		if executableBefore.exists && plistBefore.exists {
			_, _ = config.Launchctl(ctx, LaunchctlPath, []string{
				"bootstrap", "system", config.Paths.Plist,
			})
			_, _ = config.Launchctl(ctx, LaunchctlPath, []string{
				"kickstart", "-k", "system/" + wakeservice.LaunchDaemonLabel,
			})
		}
	}()

	if err := atomicWrite(config.Paths.Executable, artifact, 0555, config); err != nil {
		return err
	}
	changed = true
	plist := renderLaunchDaemonPlist(config.Paths.Executable)
	if err := atomicWrite(config.Paths.Plist, []byte(plist), 0644, config); err != nil {
		return err
	}
	metadata := wakeservice.InstallMetadata{
		StateVersion:    wakeprotocol.StateVersion,
		ProtocolVersion: wakeprotocol.Version,
		AllowedUID:      config.AllowedUID,
		DaemonBuild:     config.BuildVersion,
		ArtifactDigest:  config.ExpectedDigest,
		InstalledAt:     config.Now().UTC(),
	}
	if err := writeMetadata(metadataPath, metadata, config); err != nil {
		return err
	}
	if err := verifyInstalledLayout(config, metadata); err != nil {
		return err
	}

	// Bootout is idempotent and may fail when no prior job exists.
	_, _ = config.Launchctl(ctx, LaunchctlPath, []string{
		"bootout", "system/" + wakeservice.LaunchDaemonLabel,
	})
	if output, err := config.Launchctl(ctx, LaunchctlPath, []string{
		"bootstrap", "system", config.Paths.Plist,
	}); err != nil {
		return fmt.Errorf("bootstrap Herdr Wake LaunchDaemon: %w: %s", err, boundedStatus(string(output)))
	}
	if output, err := config.Launchctl(ctx, LaunchctlPath, []string{
		"kickstart", "-k", "system/" + wakeservice.LaunchDaemonLabel,
	}); err != nil {
		return fmt.Errorf("start Herdr Wake LaunchDaemon: %w: %s", err, boundedStatus(string(output)))
	}

	selfTestAt, err := config.SelfTest(
		ctx, config.Paths.Socket, config.BuildVersion, config.AllowedUID, config.Now,
	)
	if err != nil {
		if errors.Is(err, ErrInstallationUncertain) {
			return err
		}
		return fmt.Errorf("wake service schedule/verify/cancel self-test failed: %w", err)
	}
	metadata.LastSelfTestAt = selfTestAt.UTC()
	if err := writeMetadata(metadataPath, metadata, config); err != nil {
		return ErrInstallationUncertain
	}
	// Restart once so health reports the durable self-test timestamp.
	if output, err := config.Launchctl(ctx, LaunchctlPath, []string{
		"kickstart", "-k", "system/" + wakeservice.LaunchDaemonLabel,
	}); err != nil {
		return fmt.Errorf("restart verified Herdr Wake LaunchDaemon: %w: %s", err, boundedStatus(string(output)))
	}
	if _, err := config.HealthCheck(ctx, config.Paths.Socket, config.BuildVersion, config.AllowedUID, config.Now); err != nil {
		return fmt.Errorf("final wake service health handshake failed: %w", err)
	}
	return verifyInstalledLayout(config, metadata)
}

func defaultRootConfig(config *RootConfig) {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.EUID == nil {
		config.EUID = os.Geteuid
	}
	if config.PlatformSupported == nil {
		config.PlatformSupported = wakeservice.PlatformSupported
	}
	if config.Chown == nil {
		config.Chown = os.Chown
	}
	if config.Launchctl == nil {
		config.Launchctl = execLaunchctl
	}
	if config.SelfTest == nil {
		config.SelfTest = protocolSelfTest
	}
	if config.HealthCheck == nil {
		config.HealthCheck = healthHandshake
	}
}

func execLaunchctl(ctx context.Context, path string, arguments []string) ([]byte, error) {
	if path != LaunchctlPath {
		return nil, fmt.Errorf("refuse unexpected launchctl executable")
	}
	// #nosec G204 -- path is fixed and arguments are built from fixed launchd
	// verbs, domains, labels, and destinations.
	command := exec.CommandContext(ctx, path, arguments...)
	return command.CombinedOutput()
}

func protocolSelfTest(
	ctx context.Context,
	socket string,
	build string,
	allowedUID int,
	now func() time.Time,
) (time.Time, error) {
	if _, err := healthHandshake(ctx, socket, build, allowedUID, now); err != nil {
		return time.Time{}, err
	}
	base := now().UTC()
	wakeAt := base.Add(2 * time.Minute).Truncate(time.Second)
	candidate := wakeprotocol.Candidate{
		ID:        "installer-self-test",
		Source:    wakeprotocol.SourceContinuation,
		Purpose:   wakeprotocol.PurposeContinuation,
		WakeAt:    wakeAt,
		ExpiresAt: wakeAt.Add(10 * time.Minute),
		Reason:    "installer wake verification self-test",
	}
	register := wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "install-self-test-register",
		HelperBuild:     build,
		Operation:       wakeprotocol.OperationRegisterOrReplace,
		IdempotencyKey:  "install-self-test-register-" + strconv.FormatInt(base.Unix(), 10),
		Candidate:       &candidate,
	}
	response, err := socketRequest(ctx, socket, register)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: registration response was lost", ErrInstallationUncertain)
	}
	if response.Result != wakeprotocol.ResultSuccess {
		return time.Time{}, fmt.Errorf("registration refused: %s", response.Message)
	}
	target := wakeprotocol.Target{
		ID: candidate.ID, Source: candidate.Source, Purpose: candidate.Purpose,
	}
	cancel := wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "install-self-test-cancel",
		HelperBuild:     build,
		Operation:       wakeprotocol.OperationCancel,
		IdempotencyKey:  "install-self-test-cancel-" + strconv.FormatInt(base.Unix(), 10),
		Target:          &target,
	}
	cancelPending := true
	defer func() {
		if cancelPending {
			_, _ = socketRequest(context.Background(), socket, cancel)
		}
	}()
	verifyResponse, err := socketRequest(ctx, socket, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "install-self-test-verify",
		HelperBuild:     build,
		Operation:       wakeprotocol.OperationVerify,
		Target:          &target,
	})
	if err != nil || verifyResponse.Result != wakeprotocol.ResultSuccess ||
		verifyResponse.Verification == nil || !verifyResponse.Verification.Matched {
		return time.Time{}, fmt.Errorf("matching self-test wake could not be read back")
	}
	cancelResponse, err := socketRequest(ctx, socket, cancel)
	if err != nil || cancelResponse.Result != wakeprotocol.ResultSuccess {
		return time.Time{}, fmt.Errorf("%w: exact self-test cancellation was not proven", ErrInstallationUncertain)
	}
	cancelPending = false
	list, err := socketRequest(ctx, socket, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "install-self-test-list",
		HelperBuild:     build,
		Operation:       wakeprotocol.OperationList,
	})
	if err != nil || list.Result != wakeprotocol.ResultSuccess || list.State == nil ||
		list.State.Programmed != nil || len(list.State.Candidates) != 0 {
		return time.Time{}, fmt.Errorf("%w: self-test state was not empty after exact cancellation", ErrInstallationUncertain)
	}
	return now().UTC(), nil
}

func healthHandshake(
	ctx context.Context,
	socket string,
	build string,
	allowedUID int,
	now func() time.Time,
) (wakeprotocol.Health, error) {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := socketRequest(ctx, socket, wakeprotocol.Request{
			ProtocolVersion: wakeprotocol.Version,
			RequestID:       "install-health-" + strconv.FormatInt(now().UnixNano(), 10),
			HelperBuild:     build,
			Operation:       wakeprotocol.OperationHealth,
		})
		if err == nil && response.Result == wakeprotocol.ResultSuccess && response.Health != nil {
			if response.Health.ProtocolVersion != wakeprotocol.Version ||
				response.Health.StateVersion != wakeprotocol.StateVersion ||
				response.Health.AllowedUID != allowedUID {
				return wakeprotocol.Health{}, fmt.Errorf("daemon protocol, state, or allowed uid does not match")
			}
			return *response.Health, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return wakeprotocol.Health{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return wakeprotocol.Health{}, fmt.Errorf("timed out waiting for wake daemon: %v", lastErr)
}

func socketRequest(
	ctx context.Context,
	socket string,
	request wakeprotocol.Request,
) (wakeprotocol.Response, error) {
	dialer := &net.Dialer{Timeout: wakeservice.DefaultIOTimeout}
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return wakeprotocol.Response{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(wakeservice.DefaultIOTimeout))
	if err := wakeprotocol.WriteRequest(connection, request); err != nil {
		return wakeprotocol.Response{}, err
	}
	response, err := wakeprotocol.ReadResponse(connection)
	if err != nil {
		return wakeprotocol.Response{}, err
	}
	if err := response.Validate(); err != nil {
		return wakeprotocol.Response{}, err
	}
	return response, nil
}

type capturedFile struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func captureFile(path string, maximum int64) (capturedFile, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return capturedFile{}, nil
	}
	if err != nil {
		return capturedFile{}, fmt.Errorf("inspect existing install target: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return capturedFile{}, fmt.Errorf("refuse non-regular install target %s", path)
	}
	file, err := os.Open(path) // #nosec G304 -- fixed/test install target.
	if err != nil {
		return capturedFile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return capturedFile{}, err
	}
	if int64(len(data)) > maximum {
		return capturedFile{}, fmt.Errorf("existing install target is unexpectedly large")
	}
	return capturedFile{exists: true, data: data, mode: info.Mode().Perm()}, nil
}

func restoreCaptured(path string, captured capturedFile, config RootConfig) error {
	if !captured.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWrite(path, captured.data, captured.mode, config)
}

func readApprovedArtifact(source string) ([]byte, string, error) {
	file, err := openNoFollow(source)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 ||
		info.Mode().Perm()&0111 == 0 {
		return nil, "", fmt.Errorf("staged wake artifact is not a secure executable file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxInstalledArtifactBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxInstalledArtifactBytes {
		return nil, "", fmt.Errorf("staged wake executable exceeds %d bytes", maxInstalledArtifactBytes)
	}
	digest := sha256.Sum256(data)
	return data, fmt.Sprintf("%x", digest[:]), nil
}

func atomicWrite(destination string, data []byte, mode os.FileMode, config RootConfig) error {
	parent := filepath.Dir(destination)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("install target parent is missing or unsafe")
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse unsafe install target %s", destination)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".herdr-wake-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := config.Chown(temporaryPath, 0, 0); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	directory, err := os.Open(parent)
	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func ensureRootDirectory(path string, config RootConfig) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0700); err != nil {
			return fmt.Errorf("create root wake state directory: %w", err)
		}
		if err := config.Chown(path, 0, 0); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return fmt.Errorf("root wake state directory is unsafe")
	}
	if config.RequireRootOwnership {
		if owner, ok := ownerUID(info); !ok || owner != 0 {
			return fmt.Errorf("root wake state directory is not root-owned")
		}
	}
	return nil
}

func writeMetadata(path string, metadata wakeservice.InstallMetadata, config RootConfig) error {
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, payload, 0600, config)
}

func verifyInstalledLayout(config RootConfig, metadata wakeservice.InstallMetadata) error {
	checks := []struct {
		path string
		mode os.FileMode
	}{
		{config.Paths.Executable, 0555},
		{config.Paths.Plist, 0644},
		{filepath.Join(config.Paths.StateDir, wakeservice.InstallMetadataFile), 0600},
	}
	for _, check := range checks {
		info, err := os.Lstat(check.path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm() != check.mode {
			return fmt.Errorf("installed path %s has unexpected type or mode", check.path)
		}
		if config.RequireRootOwnership {
			if owner, ok := ownerUID(info); !ok || owner != 0 {
				return fmt.Errorf("installed path %s is not root-owned", check.path)
			}
		}
	}
	loaded, err := loadMetadataForTest(filepath.Join(config.Paths.StateDir, wakeservice.InstallMetadataFile))
	if err != nil {
		return err
	}
	if loaded.AllowedUID != metadata.AllowedUID ||
		loaded.ArtifactDigest != metadata.ArtifactDigest ||
		loaded.ProtocolVersion != wakeprotocol.Version ||
		loaded.StateVersion != wakeprotocol.StateVersion {
		return fmt.Errorf("installed metadata does not match the approved artifact")
	}
	return nil
}

func loadMetadataForTest(path string) (wakeservice.InstallMetadata, error) {
	payload, err := os.ReadFile(path) // #nosec G304 -- fixed/test metadata path.
	if err != nil {
		return wakeservice.InstallMetadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var metadata wakeservice.InstallMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return wakeservice.InstallMetadata{}, err
	}
	return metadata, nil
}

func renderLaunchDaemonPlist(executable string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + wakeservice.LaunchDaemonLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + executable + `</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>/var/log/com.ori.herdr-wake.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/com.ori.herdr-wake.log</string>
</dict>
</plist>
`
}

func validateBuild(build string) error {
	if strings.TrimSpace(build) == "" || len(build) > wakeprotocol.MaxBuildVersionLength {
		return fmt.Errorf("build version is missing or too long")
	}
	for _, character := range build {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			!strings.ContainsRune(".+_-", character) {
			return fmt.Errorf("build version is malformed")
		}
	}
	return nil
}

func removeSafeSocket(path string, allowedUID int) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to remove non-socket wake control path")
	}
	owner, ok := ownerUID(info)
	if !ok || (owner != 0 && owner != allowedUID) || info.Mode().Perm()&0177 != 0 {
		return fmt.Errorf("refuse to remove unsafe wake control socket")
	}
	return os.Remove(path)
}

func removeRootRegular(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove non-regular root path %s", path)
	}
	owner, ok := ownerUID(info)
	if !ok || owner != 0 {
		return fmt.Errorf("refuse to remove non-root-owned path %s", path)
	}
	return os.Remove(path)
}
