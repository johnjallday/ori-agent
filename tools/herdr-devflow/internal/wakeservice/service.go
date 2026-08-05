// Package wakeservice implements the narrowly scoped privileged Herdr wake
// daemon. It is intentionally independent of Ori's server, Herdr agent
// control, model providers, Git, worktrees, browsers, and system-sleep code.
package wakeservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

const (
	LaunchDaemonLabel = "com.ori.herdr-wake"
	PMSetOwner        = "com.ori.herdr-wake"
	PMSetEventType    = "wakeorpoweron"

	ExecutablePath = "/Library/PrivilegedHelperTools/com.ori.herdr-wake"
	PlistPath      = "/Library/LaunchDaemons/com.ori.herdr-wake.plist"
	SocketPath     = "/var/run/com.ori.herdr-wake.sock"
	StateDir       = "/var/db/com.ori.herdr-wake"

	InstallMetadataFile = "install.json"
	StateFile           = "state.json"
	LockFile            = "state.lock"
	AuditFile           = "audit.jsonl"

	MinimumMacOSMajor = 12
	DefaultIOTimeout  = 5 * time.Second
)

var (
	ErrUnsupported  = errors.New("the Herdr wake service is supported only on macOS 12 or newer")
	ErrRequiresRoot = errors.New("the Herdr wake daemon must run as root")
)

// InstallMetadata is the small root-owned identity written before launchd
// starts the daemon.
type InstallMetadata struct {
	StateVersion    int       `json:"state_version"`
	ProtocolVersion int       `json:"protocol_version"`
	AllowedUID      int       `json:"allowed_uid"`
	DaemonBuild     string    `json:"daemon_build"`
	ArtifactDigest  string    `json:"artifact_digest"`
	InstalledAt     time.Time `json:"installed_at"`
	LastSelfTestAt  time.Time `json:"last_self_test_at,omitempty"`
}

// Config contains production constants plus explicit test seams. Production
// uses DefaultConfig; callers cannot populate these values through IPC.
type Config struct {
	BuildVersion string
	SocketPath   string
	StateDir     string
	AllowedUID   int
	Metadata     InstallMetadata
	Now          func() time.Time
	EUID         func() int
	PeerUID      func(net.Conn) (int, error)
	Listen       func(string, string) (net.Listener, error)
	Chown        func(string, int, int) error
	Power        PowerScheduler
	IOTimeout    time.Duration
	RequireRoot  bool
}

// DefaultConfig loads no user-controlled path or owner.
func DefaultConfig(buildVersion string, metadata InstallMetadata) Config {
	return Config{
		BuildVersion: buildVersion,
		SocketPath:   SocketPath,
		StateDir:     StateDir,
		AllowedUID:   metadata.AllowedUID,
		Metadata:     metadata,
		RequireRoot:  true,
	}
}

// Service serves one framed request per authenticated Unix-socket connection.
type Service struct {
	config Config
	mu     sync.Mutex
	store  *rootStore
	power  PowerScheduler
}

// New validates fixed service identity and fills safe defaults.
func New(config Config) (*Service, error) {
	if strings.TrimSpace(config.BuildVersion) == "" {
		config.BuildVersion = "dev"
	}
	if len(config.BuildVersion) > wakeprotocol.MaxBuildVersionLength {
		return nil, fmt.Errorf("daemon build version is too long")
	}
	if config.SocketPath == "" {
		config.SocketPath = SocketPath
	}
	if config.StateDir == "" {
		config.StateDir = StateDir
	}
	if config.AllowedUID < 0 {
		return nil, fmt.Errorf("allowed uid must be non-negative")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.EUID == nil {
		config.EUID = effectiveUID
	}
	if config.PeerUID == nil {
		config.PeerUID = peerUID
	}
	if config.Listen == nil {
		config.Listen = net.Listen
	}
	if config.Chown == nil {
		config.Chown = os.Chown
	}
	if config.IOTimeout <= 0 {
		config.IOTimeout = DefaultIOTimeout
	}
	if config.Power == nil {
		power, err := newDefaultPowerScheduler(config.IOTimeout)
		if err != nil {
			return nil, err
		}
		config.Power = power
	}
	if config.Metadata.StateVersion == 0 {
		config.Metadata.StateVersion = wakeprotocol.StateVersion
	}
	if config.Metadata.ProtocolVersion == 0 {
		config.Metadata.ProtocolVersion = wakeprotocol.Version
	}
	if config.Metadata.DaemonBuild == "" {
		config.Metadata.DaemonBuild = config.BuildVersion
	}
	if config.Metadata.AllowedUID == 0 && config.AllowedUID != 0 {
		config.Metadata.AllowedUID = config.AllowedUID
	}
	return &Service{
		config: config,
		store:  newRootStore(config.StateDir, config.AllowedUID, config.RequireRoot),
		power:  config.Power,
	}, nil
}

// ServeDefault starts the production daemon from its fixed root-owned metadata.
func ServeDefault(ctx context.Context, buildVersion string) error {
	if !platformSupported() {
		return ErrUnsupported
	}
	metadata, err := LoadInstallMetadata(filepath.Join(StateDir, InstallMetadataFile))
	if err != nil {
		return fmt.Errorf("load install metadata: %w", err)
	}
	service, err := New(DefaultConfig(buildVersion, metadata))
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}

// PlatformSupported reports whether the current host satisfies the fixed v1
// macOS floor. Lifecycle code uses it before staging root-owned files.
func PlatformSupported() bool {
	return platformSupported()
}

// Serve binds only a Unix-domain socket and handles connections until context
// cancellation. It never opens a TCP listener.
func (s *Service) Serve(ctx context.Context) error {
	if !platformSupported() {
		return ErrUnsupported
	}
	if s.config.RequireRoot && s.config.EUID() != 0 {
		return ErrRequiresRoot
	}
	if err := prepareStateDir(s.config.StateDir, s.config.RequireRoot); err != nil {
		return err
	}
	if err := prepareSocketPath(s.config.SocketPath, s.config.AllowedUID); err != nil {
		return err
	}
	if err := s.reconcileStartup(ctx); err != nil {
		return fmt.Errorf("reconcile standalone wake state before serving: %w", err)
	}
	listener, err := s.config.Listen("unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on fixed wake socket: %w", err)
	}
	defer func() { _ = listener.Close() }()
	defer func() { _ = os.Remove(s.config.SocketPath) }()
	if err := os.Chmod(s.config.SocketPath, 0600); err != nil {
		return fmt.Errorf("secure wake socket: %w", err)
	}
	if err := s.config.Chown(s.config.SocketPath, s.config.AllowedUID, 0); err != nil {
		return fmt.Errorf("assign wake socket to enrolled user: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept wake client: %w", err)
		}
		s.handleConnection(ctx, connection)
	}
}

func (s *Service) handleConnection(ctx context.Context, connection net.Conn) {
	defer func() { _ = connection.Close() }()
	// Socket deadlines are real elapsed-time limits. The injected service clock
	// is for candidate decisions and may deliberately be frozen in tests.
	deadline := time.Now().Add(s.config.IOTimeout)
	_ = connection.SetDeadline(deadline)

	uid, peerErr := s.config.PeerUID(connection)
	request, readErr := wakeprotocol.ReadRequest(connection)
	if readErr != nil {
		response := s.refusal(request, wakeprotocol.ErrorCode(readErr), readErr.Error())
		_ = wakeprotocol.WriteResponse(connection, response)
		return
	}
	if peerErr != nil || (uid != 0 && uid != s.config.AllowedUID) {
		response := s.refusal(request, wakeprotocol.CodeUnauthorized, "caller uid is not authorized")
		_ = wakeprotocol.WriteResponse(connection, response)
		return
	}
	if err := request.Validate(s.config.Now()); err != nil {
		response := s.refusal(request, wakeprotocol.ErrorCode(err), err.Error())
		_ = wakeprotocol.WriteResponse(connection, response)
		return
	}
	response := s.Handle(ctx, uid, request)
	_ = wakeprotocol.WriteResponse(connection, response)
}

// Handle executes one request after rechecking authentication and validation.
// The in-process mutex plus root-owned file lock serialize every state read,
// mutation, reconciliation, and daemon restart.
func (s *Service) Handle(ctx context.Context, uid int, request wakeprotocol.Request) wakeprotocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	if uid != 0 && uid != s.config.AllowedUID {
		return s.refusal(request, wakeprotocol.CodeUnauthorized, "caller uid is not authorized")
	}
	now := s.config.Now().UTC()
	if err := request.Validate(now); err != nil {
		return s.refusal(request, wakeprotocol.ErrorCode(err), err.Error())
	}
	if request.Operation == wakeprotocol.OperationHealth {
		return wakeprotocol.Response{
			ProtocolVersion: wakeprotocol.Version,
			RequestID:       request.RequestID,
			DaemonBuild:     s.config.BuildVersion,
			Operation:       request.Operation,
			Result:          wakeprotocol.ResultSuccess,
			Code:            wakeprotocol.CodeOK,
			Message:         "wake service is healthy",
			Health: &wakeprotocol.Health{
				Installed:       true,
				Running:         true,
				ProtocolVersion: wakeprotocol.Version,
				StateVersion:    wakeprotocol.StateVersion,
				DaemonBuild:     s.config.BuildVersion,
				AllowedUID:      s.config.AllowedUID,
				CheckedAt:       now,
				LastSelfTestAt:  s.config.Metadata.LastSelfTestAt,
			},
		}
	}

	unlock, err := s.store.lock(ctx)
	if err != nil {
		return s.refusal(request, wakeprotocol.CodeStateFailed, err.Error())
	}
	defer unlock()

	state, err := s.store.load()
	if err != nil {
		return s.refusal(request, wakeprotocol.CodeStateFailed, err.Error())
	}
	var response wakeprotocol.Response
	switch request.Operation {
	case wakeprotocol.OperationRegisterOrReplace:
		response = s.register(ctx, uid, request, state, now)
	case wakeprotocol.OperationCancel:
		response = s.cancel(ctx, uid, request, state, now)
	case wakeprotocol.OperationList:
		public := state.public()
		response = s.success(request, "wake state listed", &public, nil)
	case wakeprotocol.OperationVerify:
		response = s.verify(ctx, request, state, now)
	default:
		response = s.refusal(request, wakeprotocol.CodeUnsupported, "operation is not implemented by this daemon build")
	}
	s.recordAudit(uid, request, response, now)
	return response
}

func (s *Service) success(
	request wakeprotocol.Request,
	message string,
	state *wakeprotocol.State,
	verification *wakeprotocol.Verification,
) wakeprotocol.Response {
	return wakeprotocol.Response{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       request.RequestID,
		DaemonBuild:     s.config.BuildVersion,
		Operation:       request.Operation,
		Result:          wakeprotocol.ResultSuccess,
		Code:            wakeprotocol.CodeOK,
		Message:         boundedMessage(message),
		State:           state,
		Verification:    verification,
	}
}

func (s *Service) uncertain(request wakeprotocol.Request, message string) wakeprotocol.Response {
	return wakeprotocol.Response{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       request.RequestID,
		DaemonBuild:     s.config.BuildVersion,
		Operation:       request.Operation,
		Result:          wakeprotocol.ResultUncertain,
		Code:            wakeprotocol.CodeUncertain,
		Message:         boundedMessage(message),
	}
}

func (s *Service) refusal(request wakeprotocol.Request, code wakeprotocol.Code, message string) wakeprotocol.Response {
	if request.RequestID == "" {
		request.RequestID = "invalid-request"
	}
	if request.Operation == "" {
		request.Operation = wakeprotocol.OperationHealth
	}
	return wakeprotocol.Response{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       request.RequestID,
		DaemonBuild:     s.config.BuildVersion,
		Operation:       request.Operation,
		Result:          wakeprotocol.ResultRefusal,
		Code:            code,
		Message:         boundedMessage(message),
	}
}

func boundedMessage(message string) string {
	message = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	if len(message) > wakeprotocol.MaxMessageLength {
		message = message[:wakeprotocol.MaxMessageLength]
	}
	return message
}

// LoadInstallMetadata reads one bounded, strict root-state identity.
func LoadInstallMetadata(path string) (InstallMetadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return InstallMetadata{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return InstallMetadata{}, fmt.Errorf("install metadata is not a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return InstallMetadata{}, fmt.Errorf("install metadata permissions are broader than 0600")
	}
	if owner, ok := fileOwnerUID(info); !ok || owner != 0 {
		return InstallMetadata{}, fmt.Errorf("install metadata is not root-owned")
	}
	file, err := os.Open(path) // #nosec G304 -- production passes the fixed state path.
	if err != nil {
		return InstallMetadata{}, err
	}
	defer func() { _ = file.Close() }()
	payload, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil {
		return InstallMetadata{}, fmt.Errorf("read install metadata: %w", err)
	}
	if len(payload) > 16*1024 {
		return InstallMetadata{}, fmt.Errorf("install metadata exceeds 16 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var metadata InstallMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return InstallMetadata{}, fmt.Errorf("decode install metadata: %w", err)
	}
	if metadata.StateVersion != wakeprotocol.StateVersion ||
		metadata.ProtocolVersion != wakeprotocol.Version ||
		metadata.AllowedUID < 0 ||
		strings.TrimSpace(metadata.DaemonBuild) == "" {
		return InstallMetadata{}, fmt.Errorf("install metadata is incompatible or incomplete")
	}
	return metadata, nil
}

func prepareSocketPath(path string, allowedUID int) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect wake socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to replace non-socket at %s", path)
	}
	owner, ok := fileOwnerUID(info)
	if !ok || (owner != 0 && owner != allowedUID) {
		return fmt.Errorf("refuse to replace wake socket with unexpected owner")
	}
	if info.Mode().Perm()&0177 != 0 {
		return fmt.Errorf("refuse to replace wake socket with unsafe permissions")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale wake socket: %w", err)
	}
	return nil
}

func prepareStateDir(path string, requireRoot bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create private wake state directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect private wake state directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private wake state path is not a directory")
	}
	if info.Mode().Perm() != 0700 {
		return fmt.Errorf("private wake state directory must use mode 0700")
	}
	if requireRoot {
		if owner, ok := fileOwnerUID(info); !ok || owner != 0 {
			return fmt.Errorf("private wake state directory must be root-owned")
		}
	}
	return nil
}
