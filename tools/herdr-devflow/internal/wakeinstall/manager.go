// Package wakeinstall implements the explicit administrator-approved
// lifecycle for the standalone Herdr Wake Service. User-level preparation and
// root-only installation share typed operations but never invoke a shell.
package wakeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
)

const (
	SudoPath      = "/usr/bin/sudo"
	LaunchctlPath = "/bin/launchctl"
)

// PreparedInstall is a user-private staged artifact. Cleanup must be called
// regardless of whether confirmation or administrator authorization succeeds.
type PreparedInstall struct {
	ArtifactPath   string `json:"artifact_path"`
	ArtifactDigest string `json:"artifact_digest"`
	BuildVersion   string `json:"build_version"`
	AllowedUID     int    `json:"allowed_uid"`
	stagingDir     string
}

// Cleanup removes only the newly created private staging directory.
func (p PreparedInstall) Cleanup() error {
	if p.stagingDir == "" {
		return nil
	}
	return os.RemoveAll(p.stagingDir)
}

// Status is the bounded public lifecycle view.
type Status struct {
	Supported       bool      `json:"supported"`
	Installed       bool      `json:"installed"`
	Running         bool      `json:"running"`
	Compatible      bool      `json:"compatible"`
	AllowedUID      int       `json:"allowed_uid,omitempty"`
	ProtocolVersion int       `json:"protocol_version,omitempty"`
	StateVersion    int       `json:"state_version,omitempty"`
	DaemonBuild     string    `json:"daemon_build,omitempty"`
	LastSelfTestAt  time.Time `json:"last_self_test_at,omitempty"`
	Detail          string    `json:"detail,omitempty"`
}

// Diagnostic is one stable doctor result.
type Diagnostic struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Recovery string `json:"recovery,omitempty"`
}

// Manager owns the unprivileged staging, status, and sudo boundary.
type Manager struct {
	GOOS         string
	BuildVersion string
	SocketPath   string
	Build        func(context.Context, string, string, string) error
	Sudo         func(context.Context, string, []string) error
	TempDir      func(string, string) (string, error)
	Dial         func(context.Context, string) (net.Conn, error)
	StatusCheck  func(context.Context) (Status, error)
}

// NewManager returns the production lifecycle manager.
func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) defaults() {
	if m.GOOS == "" {
		m.GOOS = runtime.GOOS
	}
	if m.BuildVersion == "" {
		m.BuildVersion = "dev"
	}
	if m.SocketPath == "" {
		m.SocketPath = wakeservice.SocketPath
	}
	if m.Build == nil {
		m.Build = buildDaemon
	}
	if m.Sudo == nil {
		m.Sudo = runSudo
	}
	if m.TempDir == nil {
		m.TempDir = os.MkdirTemp
	}
	if m.Dial == nil {
		m.Dial = dialSocket
	}
}

// PrepareInstall builds the matching daemon in a newly created private
// staging directory and returns its digest for the pre-authorization preview.
func (m *Manager) PrepareInstall(
	ctx context.Context,
	repoRoot string,
	allowedUID int,
) (PreparedInstall, error) {
	m.defaults()
	if m.GOOS != "darwin" {
		return PreparedInstall{}, wakeservice.ErrUnsupported
	}
	if allowedUID <= 0 {
		return PreparedInstall{}, fmt.Errorf("an enrolled non-root macOS uid is required")
	}
	if strings.TrimSpace(repoRoot) == "" {
		return PreparedInstall{}, fmt.Errorf("repository root is required to build the wake artifact")
	}
	staging, err := m.TempDir("", "herdr-wake-install-")
	if err != nil {
		return PreparedInstall{}, fmt.Errorf("create private wake staging directory: %w", err)
	}
	if err := os.Chmod(staging, 0700); err != nil {
		_ = os.RemoveAll(staging)
		return PreparedInstall{}, fmt.Errorf("secure wake staging directory: %w", err)
	}
	artifact := filepath.Join(staging, "herdr-wake")
	if err := m.Build(ctx, repoRoot, artifact, m.BuildVersion); err != nil {
		_ = os.RemoveAll(staging)
		return PreparedInstall{}, fmt.Errorf("build staged Herdr wake daemon: %w", err)
	}
	if err := validateStagedArtifact(artifact); err != nil {
		_ = os.RemoveAll(staging)
		return PreparedInstall{}, err
	}
	digest, err := fileDigest(artifact)
	if err != nil {
		_ = os.RemoveAll(staging)
		return PreparedInstall{}, err
	}
	return PreparedInstall{
		ArtifactPath:   artifact,
		ArtifactDigest: digest,
		BuildVersion:   m.BuildVersion,
		AllowedUID:     allowedUID,
		stagingDir:     staging,
	}, nil
}

// Install crosses the one approved sudo boundary with a fixed argument shape.
func (m *Manager) Install(ctx context.Context, prepared PreparedInstall) (Status, error) {
	m.defaults()
	if m.GOOS != "darwin" {
		return Status{}, wakeservice.ErrUnsupported
	}
	if err := validatePrepared(prepared); err != nil {
		return Status{}, err
	}
	arguments := []string{
		"-k",
		"--",
		prepared.ArtifactPath,
		"install",
		"--allowed-uid", strconv.Itoa(prepared.AllowedUID),
		"--artifact-digest", prepared.ArtifactDigest,
		"--build", prepared.BuildVersion,
	}
	if err := m.Sudo(ctx, SudoPath, arguments); err != nil {
		return Status{}, fmt.Errorf("administrator installation failed: %w", err)
	}
	status, err := m.Status(ctx)
	if err != nil {
		return status, err
	}
	if !status.Installed || !status.Running || !status.Compatible ||
		status.AllowedUID != prepared.AllowedUID {
		return status, fmt.Errorf("installed wake service did not pass the health and uid handshake")
	}
	return status, nil
}

// Uninstall invokes only the fixed installed executable and typed root verb.
func (m *Manager) Uninstall(ctx context.Context, allowedUID int) (Status, error) {
	m.defaults()
	if m.GOOS != "darwin" {
		return Status{}, wakeservice.ErrUnsupported
	}
	if allowedUID <= 0 {
		return Status{}, fmt.Errorf("an enrolled non-root macOS uid is required")
	}
	current, statusErr := m.Status(ctx)
	if statusErr != nil {
		return current, statusErr
	}
	if !current.Installed {
		return current, nil
	}
	arguments := []string{
		"-k",
		"--",
		wakeservice.ExecutablePath,
		"uninstall",
		"--allowed-uid", strconv.Itoa(allowedUID),
	}
	if err := m.Sudo(ctx, SudoPath, arguments); err != nil {
		return Status{}, fmt.Errorf("administrator uninstall failed: %w", err)
	}
	status, err := m.Status(ctx)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	if status.Installed || status.Running {
		return status, fmt.Errorf("wake service still appears installed after uninstall")
	}
	return status, nil
}

// Status checks fixed artifacts and performs a bounded protocol handshake.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.defaults()
	if m.StatusCheck != nil {
		return m.StatusCheck(ctx)
	}
	status := Status{Supported: m.GOOS == "darwin"}
	if !status.Supported {
		status.Detail = wakeservice.ErrUnsupported.Error()
		return status, nil
	}
	executable, executableErr := os.Lstat(wakeservice.ExecutablePath)
	plist, plistErr := os.Lstat(wakeservice.PlistPath)
	status.Installed = executableErr == nil && plistErr == nil &&
		executable.Mode().IsRegular() && plist.Mode().IsRegular()
	if !status.Installed {
		status.Detail = "standalone Herdr wake service is not installed"
		return status, nil
	}
	response, err := m.request(ctx, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "wake-status",
		HelperBuild:     m.BuildVersion,
		Operation:       wakeprotocol.OperationHealth,
	})
	if err != nil {
		status.Detail = "installed files exist but the local wake daemon did not answer"
		return status, nil
	}
	if response.Result != wakeprotocol.ResultSuccess || response.Health == nil {
		status.Detail = boundedStatus(response.Message)
		return status, nil
	}
	status.Running = response.Health.Running
	status.ProtocolVersion = response.Health.ProtocolVersion
	status.StateVersion = response.Health.StateVersion
	status.DaemonBuild = response.Health.DaemonBuild
	status.AllowedUID = response.Health.AllowedUID
	status.LastSelfTestAt = response.Health.LastSelfTestAt
	status.Compatible = response.ProtocolVersion == wakeprotocol.Version &&
		response.Health.ProtocolVersion == wakeprotocol.Version &&
		response.Health.StateVersion == wakeprotocol.StateVersion
	if status.Compatible {
		status.Detail = "standalone Herdr wake service is healthy"
	} else {
		status.Detail = "wake service protocol or state version is incompatible"
	}
	return status, nil
}

// Doctor expands status into stable, actionable checks.
func (m *Manager) Doctor(ctx context.Context) ([]Diagnostic, error) {
	m.defaults()
	status, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}
	diagnostics := []Diagnostic{{
		Name: "platform", Status: "PASS", Detail: "macOS 12 or newer is required",
	}}
	if !status.Supported {
		diagnostics[0].Status = "WARN"
		diagnostics[0].Detail = status.Detail
		diagnostics[0].Recovery = "use --stay-awake or run wake-enabled Herdr workflows on supported macOS"
		return diagnostics, nil
	}
	if status.Installed {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "installation", Status: "PASS",
			Detail: wakeservice.ExecutablePath + " and " + wakeservice.PlistPath,
		})
	} else {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "installation", Status: "FAIL", Detail: status.Detail,
			Recovery: "wt herd wake install",
		})
		return diagnostics, nil
	}
	if status.Running && status.Compatible {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "health", Status: "PASS",
			Detail: fmt.Sprintf("protocol %d, build %s, allowed uid %d",
				status.ProtocolVersion, status.DaemonBuild, status.AllowedUID),
		})
	} else {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "health", Status: "FAIL", Detail: status.Detail,
			Recovery: "wt herd wake install",
		})
	}
	if !status.LastSelfTestAt.IsZero() {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "self-test", Status: "PASS", Detail: status.LastSelfTestAt.Format(time.RFC3339),
		})
	} else {
		diagnostics = append(diagnostics, Diagnostic{
			Name: "self-test", Status: "WARN", Detail: "no successful installer self-test timestamp was reported",
			Recovery: "wt herd wake install",
		})
	}
	return diagnostics, nil
}

func (m *Manager) request(ctx context.Context, request wakeprotocol.Request) (wakeprotocol.Response, error) {
	connection, err := m.Dial(ctx, m.SocketPath)
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

func buildDaemon(ctx context.Context, repoRoot, destination, buildVersion string) error {
	// #nosec G204 -- executable, package, and ldflag symbol are fixed; paths are
	// resolved by the repository/runtime layer and passed as individual argv.
	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-trimpath",
		"-ldflags",
		"-X main.buildVersion="+buildVersion,
		"-o",
		destination,
		"./tools/herdr-devflow/cmd/herdr-wake",
	)
	command.Dir = repoRoot
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, boundedStatus(string(output)))
	}
	return nil
}

func runSudo(ctx context.Context, path string, arguments []string) error {
	if path != SudoPath {
		return fmt.Errorf("refuse unexpected administrator executable")
	}
	// #nosec G204 -- path and argument shape are fixed and validated above.
	command := exec.CommandContext(ctx, path, arguments...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: wakeservice.DefaultIOTimeout}
	return dialer.DialContext(ctx, "unix", path)
}

func validateStagedArtifact(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect staged wake artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged wake artifact is not a regular file")
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("staged wake artifact is group- or world-writable")
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("staged wake artifact is not executable")
	}
	return nil
}

func validatePrepared(prepared PreparedInstall) error {
	if prepared.AllowedUID <= 0 ||
		strings.TrimSpace(prepared.BuildVersion) == "" ||
		len(prepared.BuildVersion) > wakeprotocol.MaxBuildVersionLength ||
		len(prepared.ArtifactDigest) != sha256.Size*2 {
		return fmt.Errorf("prepared wake installation is incomplete")
	}
	if err := validateStagedArtifact(prepared.ArtifactPath); err != nil {
		return err
	}
	digest, err := fileDigest(prepared.ArtifactPath)
	if err != nil {
		return err
	}
	if digest != prepared.ArtifactDigest {
		return fmt.Errorf("staged wake artifact changed after preview")
	}
	return nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- caller validates the typed staged/fixed path.
	if err != nil {
		return "", fmt.Errorf("open wake artifact for digest: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest wake artifact: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func boundedStatus(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value))
	if len(value) > wakeprotocol.MaxMessageLength {
		value = value[:wakeprotocol.MaxMessageLength]
	}
	return value
}
