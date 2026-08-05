package wakeinstall

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
)

func TestManagerStagesDigestAndUsesOneFixedSudoBoundary(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	var sudoPath string
	var sudoArguments []string
	manager := &Manager{
		GOOS:         "darwin",
		BuildVersion: "test-build",
		Build: func(_ context.Context, gotRepo, destination, build string) error {
			if gotRepo != repo || build != "test-build" {
				t.Fatalf("build inputs = %q %q", gotRepo, build)
			}
			return os.WriteFile(destination, []byte("daemon"), 0755)
		},
		Sudo: func(_ context.Context, path string, arguments []string) error {
			sudoPath = path
			sudoArguments = append([]string(nil), arguments...)
			return nil
		},
		StatusCheck: func(context.Context) (Status, error) {
			return Status{
				Supported: true, Installed: true, Running: true, Compatible: true,
				AllowedUID: 501,
			}, nil
		},
	}
	prepared, err := manager.PrepareInstall(context.Background(), repo, 501)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Cleanup() }()
	if prepared.ArtifactDigest != "f77b12a53ece5f6b7050800bbdbf8cc5ebe87f1b1387cf739f243e43e2ce886b" {
		t.Fatalf("artifact digest = %s", prepared.ArtifactDigest)
	}
	if _, err := manager.Install(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-k", "--", prepared.ArtifactPath, "install",
		"--allowed-uid", "501",
		"--artifact-digest", prepared.ArtifactDigest,
		"--build", "test-build",
	}
	if sudoPath != SudoPath || !reflect.DeepEqual(sudoArguments, want) {
		t.Fatalf("sudo invocation = %q %#v, want %q %#v", sudoPath, sudoArguments, SudoPath, want)
	}
	for _, argument := range sudoArguments {
		if argument == "-n" || strings.Contains(argument, "askpass") {
			t.Fatalf("administrator boundary contained forbidden argument %q", argument)
		}
	}
}

func TestInstallRootWritesAtomicFixedLayoutAndIsIdempotent(t *testing.T) {
	t.Parallel()
	fixture := newRootFixture(t)
	var launchCalls [][]string
	fixture.config.Launchctl = func(_ context.Context, path string, arguments []string) ([]byte, error) {
		if path != LaunchctlPath {
			t.Fatalf("launchctl path = %q", path)
		}
		launchCalls = append(launchCalls, append([]string(nil), arguments...))
		return nil, nil
	}
	selfTests := 0
	fixture.config.SelfTest = func(
		_ context.Context, socket, build string, uid int, _ func() time.Time,
	) (time.Time, error) {
		selfTests++
		if socket != fixture.paths.Socket || build != "test-build" || uid != 501 {
			t.Fatalf("self-test identity = %q %q %d", socket, build, uid)
		}
		return fixture.now.Add(time.Minute), nil
	}
	fixture.config.HealthCheck = func(
		context.Context, string, string, int, func() time.Time,
	) (wakeprotocol.Health, error) {
		return wakeprotocol.Health{AllowedUID: 501}, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := InstallRoot(context.Background(), fixture.config); err != nil {
			t.Fatalf("InstallRoot() attempt %d error = %v", attempt+1, err)
		}
	}
	if selfTests != 2 {
		t.Fatalf("self-tests = %d, want 2", selfTests)
	}
	assertMode(t, fixture.paths.Executable, 0555)
	assertMode(t, fixture.paths.Plist, 0644)
	assertMode(t, filepath.Join(fixture.paths.StateDir, wakeservice.InstallMetadataFile), 0600)
	assertMode(t, fixture.paths.StateDir, 0700)
	installed, err := os.ReadFile(fixture.paths.Executable)
	if err != nil || string(installed) != "new daemon bytes" {
		t.Fatalf("installed executable = %q, %v", installed, err)
	}
	plist, err := os.ReadFile(fixture.paths.Plist)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), fixture.paths.Executable) ||
		!strings.Contains(string(plist), "<string>serve</string>") ||
		!strings.Contains(string(plist), wakeservice.LaunchDaemonLabel) {
		t.Fatalf("installed plist = %s", plist)
	}
	metadata, err := loadMetadataForTest(filepath.Join(fixture.paths.StateDir, wakeservice.InstallMetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.AllowedUID != 501 || metadata.DaemonBuild != "test-build" ||
		metadata.ArtifactDigest != fixture.config.ExpectedDigest ||
		!metadata.LastSelfTestAt.Equal(fixture.now.Add(time.Minute)) {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(launchCalls) != 8 {
		t.Fatalf("launchctl calls = %#v, want four per install", launchCalls)
	}
}

func TestInstallRootRollsBackFilesWhenBootstrapFails(t *testing.T) {
	t.Parallel()
	fixture := newRootFixture(t)
	metadataPath := filepath.Join(fixture.paths.StateDir, wakeservice.InstallMetadataFile)
	if err := os.Mkdir(fixture.paths.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		fixture.paths.Executable: "old executable",
		fixture.paths.Plist:      "old plist",
		metadataPath:             `{"old":"metadata"}`,
	} {
		mode := os.FileMode(0644)
		if path == fixture.paths.Executable {
			mode = 0555
		} else if path == metadataPath {
			mode = 0600
		}
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	fixture.config.Launchctl = func(_ context.Context, _ string, arguments []string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "bootstrap" {
			return []byte("bootstrap refused"), errors.New("exit 5")
		}
		return nil, nil
	}
	if err := InstallRoot(context.Background(), fixture.config); err == nil {
		t.Fatal("InstallRoot() succeeded despite bootstrap failure")
	}
	for path, want := range map[string]string{
		fixture.paths.Executable: "old executable",
		fixture.paths.Plist:      "old plist",
		metadataPath:             `{"old":"metadata"}`,
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("rollback %s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestInstallRootRefusesNonRootAndSymlinkTargetsBeforeLaunchctl(t *testing.T) {
	t.Parallel()
	fixture := newRootFixture(t)
	launchCalls := 0
	fixture.config.Launchctl = func(context.Context, string, []string) ([]byte, error) {
		launchCalls++
		return nil, nil
	}
	fixture.config.EUID = func() int { return 501 }
	if err := InstallRoot(context.Background(), fixture.config); !errors.Is(err, wakeservice.ErrRequiresRoot) {
		t.Fatalf("non-root error = %v", err)
	}
	fixture.config.EUID = func() int { return 0 }
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, fixture.paths.Executable); err != nil {
		t.Fatal(err)
	}
	if err := InstallRoot(context.Background(), fixture.config); err == nil {
		t.Fatalf("symlink error = %v", err)
	}
	if launchCalls != 0 {
		t.Fatalf("launchctl calls = %d, want 0", launchCalls)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("symlink target changed: %q, %v", contents, err)
	}
}

func TestDoctorReportsCandidateInventoryAndProgrammedWake(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	manager := &Manager{StatusCheck: func(context.Context) (Status, error) {
		return Status{
			Supported: true, Installed: true, Running: true, Compatible: true,
			LastSelfTestAt: now,
			WakeState: &wakeprotocol.State{
				ReconciledAt: now,
				Candidates: []wakeprotocol.Candidate{{
					ID: "overnight-1", Source: wakeprotocol.SourceOvernight,
					Purpose: wakeprotocol.PurposeClaudeReset, WakeAt: now.Add(time.Hour),
				}},
				Programmed: &wakeprotocol.Programmed{
					Target: wakeprotocol.Target{ID: "overnight-1", Source: wakeprotocol.SourceOvernight, Purpose: wakeprotocol.PurposeClaudeReset},
					WakeAt: now.Add(time.Hour), Owner: "com.ori.herdr-wake", EventType: "wakeorpoweron",
				},
			},
		}, nil
	}}
	diagnostics, err := manager.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var inventory, programmed bool
	for _, diagnostic := range diagnostics {
		inventory = inventory || diagnostic.Name == "candidate inventory" && diagnostic.Status == "PASS"
		programmed = programmed || diagnostic.Name == "programmed wake" && diagnostic.Status == "PASS"
	}
	if !inventory || !programmed {
		t.Fatalf("diagnostics = %#v, want candidate inventory and programmed wake", diagnostics)
	}
}

func TestProtocolSelfTestRegistersVerifiesAndLeavesNoWake(t *testing.T) {
	socket := filepath.Join(shortSocketDir(t), "wake.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	var operations []wakeprotocol.Operation
	serverErrors := make(chan error, 1)
	go func() {
		var candidate wakeprotocol.Candidate
		for index := 0; index < 5; index++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverErrors <- acceptErr
				return
			}
			request, readErr := wakeprotocol.ReadRequest(connection)
			if readErr != nil {
				_ = connection.Close()
				serverErrors <- readErr
				return
			}
			operations = append(operations, request.Operation)
			response := wakeprotocol.Response{
				ProtocolVersion: wakeprotocol.Version,
				RequestID:       request.RequestID, DaemonBuild: "test",
				Operation: request.Operation, Result: wakeprotocol.ResultSuccess,
				Code: wakeprotocol.CodeOK,
			}
			switch request.Operation {
			case wakeprotocol.OperationHealth:
				response.Health = &wakeprotocol.Health{
					Running: true, ProtocolVersion: wakeprotocol.Version,
					StateVersion: wakeprotocol.StateVersion, AllowedUID: 501,
				}
			case wakeprotocol.OperationRegisterOrReplace:
				candidate = *request.Candidate
			case wakeprotocol.OperationVerify:
				response.Verification = &wakeprotocol.Verification{
					Target: *request.Target, RequestedWakeAt: candidate.WakeAt,
					ProgrammedWakeAt: candidate.WakeAt, Matched: true,
				}
			case wakeprotocol.OperationList:
				response.State = &wakeprotocol.State{
					StateVersion: wakeprotocol.StateVersion, AllowedUID: 501,
					Candidates: []wakeprotocol.Candidate{},
				}
			}
			writeErr := wakeprotocol.WriteResponse(connection, response)
			_ = connection.Close()
			if writeErr != nil {
				serverErrors <- writeErr
				return
			}
		}
		serverErrors <- nil
	}()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	if _, err := protocolSelfTest(
		context.Background(), socket, "test", 501, func() time.Time { return now },
	); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
	want := []wakeprotocol.Operation{
		wakeprotocol.OperationHealth,
		wakeprotocol.OperationRegisterOrReplace,
		wakeprotocol.OperationVerify,
		wakeprotocol.OperationCancel,
		wakeprotocol.OperationList,
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("self-test operations = %v, want %v", operations, want)
	}
}

type rootFixture struct {
	config RootConfig
	paths  RootPaths
	now    time.Time
}

func newRootFixture(t *testing.T) rootFixture {
	t.Helper()
	root := t.TempDir()
	helpers := filepath.Join(root, "Library", "PrivilegedHelperTools")
	daemons := filepath.Join(root, "Library", "LaunchDaemons")
	for _, directory := range []string{helpers, daemons} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "staged-herdr-wake")
	if err := os.WriteFile(source, []byte("new daemon bytes"), 0755); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	paths := RootPaths{
		Executable: filepath.Join(helpers, "com.ori.herdr-wake"),
		Plist:      filepath.Join(daemons, "com.ori.herdr-wake.plist"),
		Socket:     filepath.Join(root, "var", "run", "com.ori.herdr-wake.sock"),
		StateDir:   filepath.Join(root, "var", "db", "com.ori.herdr-wake"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.StateDir), 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	return rootFixture{
		paths: paths,
		now:   now,
		config: RootConfig{
			Paths: paths, SourcePath: source, AllowedUID: 501,
			ExpectedDigest: digest, BuildVersion: "test-build",
			Now: func() time.Time { return now }, EUID: func() int { return 0 },
			PlatformSupported:    func() bool { return true },
			Chown:                func(string, int, int) error { return nil },
			RequireRootOwnership: false,
		},
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	// Darwin needs a short Unix-socket path, while Linux and Windows runners do
	// not expose macOS's /private/tmp alias. Prefer it when present and fall
	// back to the portable process temp directory for cross-platform tests.
	parent := os.TempDir()
	if _, err := os.Stat("/private/tmp"); err == nil {
		parent = "/private/tmp"
	}
	directory, err := os.MkdirTemp(parent, "hwi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestInstalledMetadataJSONContainsNoCredentialOrRepositoryFields(t *testing.T) {
	t.Parallel()
	metadata := wakeservice.InstallMetadata{
		StateVersion: 1, ProtocolVersion: 1, AllowedUID: 501,
		DaemonBuild: "test", ArtifactDigest: strings.Repeat("a", 64),
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "token", "authorization", "repository", "worktree", "prompt"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("metadata contains forbidden field %q: %s", forbidden, payload)
		}
	}
}
