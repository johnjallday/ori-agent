package wakeservice

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

func TestHealthResponseReportsFixedIdentityAndCompatibility(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	service, err := New(Config{
		BuildVersion: "1.2.3",
		AllowedUID:   501,
		Now:          func() time.Time { return now },
		Metadata: InstallMetadata{
			StateVersion:    wakeprotocol.StateVersion,
			ProtocolVersion: wakeprotocol.Version,
			AllowedUID:      501,
			DaemonBuild:     "1.2.3",
			LastSelfTestAt:  now.Add(-time.Hour),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "req-health",
		HelperBuild:     "1.2.4",
		Operation:       wakeprotocol.OperationHealth,
	}
	response := service.Handle(context.Background(), 501, request)
	if err := response.Validate(); err != nil {
		t.Fatalf("response Validate() error = %v", err)
	}
	if response.Result != wakeprotocol.ResultSuccess || response.Health == nil {
		t.Fatalf("health response = %+v", response)
	}
	if response.Health.AllowedUID != 501 ||
		response.Health.ProtocolVersion != wakeprotocol.Version ||
		response.Health.StateVersion != wakeprotocol.StateVersion ||
		response.Health.DaemonBuild != "1.2.3" ||
		!response.Health.LastSelfTestAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("health identity = %+v", response.Health)
	}
}

func TestServeHealthAuthenticatesEnrolledUIDAndSecuresSocket(t *testing.T) {
	if !platformSupported() {
		t.Skip("Unix peer-credential wake service is macOS-only")
	}
	socket := filepath.Join(shortTempDir(t), "wake.sock")
	stateDir := filepath.Join(t.TempDir(), "state")
	allowedUID := os.Getuid()
	service, err := New(Config{
		BuildVersion: "dev",
		SocketPath:   socket,
		StateDir:     stateDir,
		AllowedUID:   allowedUID,
		Power:        &fakePowerScheduler{},
		RequireRoot:  false,
		PeerUID:      func(net.Conn) (int, error) { return allowedUID, nil },
		Chown:        func(string, int, int) error { return nil },
		IOTimeout:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel, serveErrors := startTestServer(t, service, socket)
	defer cancel()

	info, err := os.Lstat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("socket mode = %04o, want 0600", got)
	}
	stateInfo, err := os.Lstat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stateInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("state directory mode = %04o, want 0700", got)
	}

	response := roundTrip(t, socket, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "req-health",
		HelperBuild:     "different-compatible-build",
		Operation:       wakeprotocol.OperationHealth,
	})
	if response.Result != wakeprotocol.ResultSuccess || response.Code != wakeprotocol.CodeOK {
		t.Fatalf("health response = %+v", response)
	}

	mismatch := roundTrip(t, socket, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version + 1,
		RequestID:       "req-mismatch",
		HelperBuild:     "dev",
		Operation:       wakeprotocol.OperationHealth,
	})
	if mismatch.Result != wakeprotocol.ResultRefusal || mismatch.Code != wakeprotocol.CodeIncompatibleProtocol {
		t.Fatalf("mismatch response = %+v", mismatch)
	}

	cancel()
	if err := <-serveErrors; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestServeRejectsUnauthorizedPeerBeforeHandlingHealth(t *testing.T) {
	if !platformSupported() {
		t.Skip("Unix peer-credential wake service is macOS-only")
	}
	socket := filepath.Join(shortTempDir(t), "wake.sock")
	service, err := New(Config{
		BuildVersion: "dev",
		SocketPath:   socket,
		StateDir:     filepath.Join(t.TempDir(), "state"),
		AllowedUID:   os.Getuid(),
		Power:        &fakePowerScheduler{},
		RequireRoot:  false,
		PeerUID:      func(net.Conn) (int, error) { return os.Getuid() + 1, nil },
		Chown:        func(string, int, int) error { return nil },
		IOTimeout:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel, serveErrors := startTestServer(t, service, socket)
	response := roundTrip(t, socket, wakeprotocol.Request{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       "req-health",
		HelperBuild:     "dev",
		Operation:       wakeprotocol.OperationHealth,
	})
	if response.Result != wakeprotocol.ResultRefusal || response.Code != wakeprotocol.CodeUnauthorized {
		t.Fatalf("unauthorized response = %+v", response)
	}
	cancel()
	if err := <-serveErrors; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestServeRefusesUnsafeSocketAndStatePaths(t *testing.T) {
	if !platformSupported() {
		t.Skip("Unix wake service is macOS-only")
	}
	t.Run("regular file at socket path", func(t *testing.T) {
		socket := filepath.Join(shortTempDir(t), "wake.sock")
		if err := os.WriteFile(socket, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		service, err := New(testConfig(socket, filepath.Join(t.TempDir(), "state")))
		if err != nil {
			t.Fatal(err)
		}
		err = service.Serve(context.Background())
		if err == nil || !strings.Contains(err.Error(), "non-socket") {
			t.Fatalf("Serve() error = %v, want non-socket refusal", err)
		}
		if contents, readErr := os.ReadFile(socket); readErr != nil || string(contents) != "keep" {
			t.Fatalf("unsafe socket target changed: %q, %v", contents, readErr)
		}
	})

	t.Run("broad state directory", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(stateDir, 0755); err != nil {
			t.Fatal(err)
		}
		service, err := New(testConfig(filepath.Join(shortTempDir(t), "wake.sock"), stateDir))
		if err != nil {
			t.Fatal(err)
		}
		err = service.Serve(context.Background())
		if err == nil || !strings.Contains(err.Error(), "mode 0700") {
			t.Fatalf("Serve() error = %v, want private-state refusal", err)
		}
	})
}

func testConfig(socket, stateDir string) Config {
	return Config{
		BuildVersion: "dev",
		SocketPath:   socket,
		StateDir:     stateDir,
		AllowedUID:   os.Getuid(),
		Power:        &fakePowerScheduler{},
		RequireRoot:  false,
		PeerUID:      func(net.Conn) (int, error) { return os.Getuid(), nil },
		Chown:        func(string, int, int) error { return nil },
		IOTimeout:    100 * time.Millisecond,
	}
}

func startTestServer(t *testing.T, service *Service, socket string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errorsChannel := make(chan error, 1)
	go func() {
		errorsChannel <- service.Serve(ctx)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			return cancel, errorsChannel
		}
		select {
		case err := <-errorsChannel:
			cancel()
			t.Fatalf("Serve() exited before socket creation: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for wake socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func roundTrip(t *testing.T, socket string, request wakeprotocol.Request) wakeprotocol.Response {
	t.Helper()
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := wakeprotocol.WriteRequest(connection, request); err != nil {
		t.Fatal(err)
	}
	response, err := wakeprotocol.ReadResponse(connection)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRequiresRootWhenProductionGuardIsEnabled(t *testing.T) {
	if !platformSupported() {
		t.Skip("root LaunchDaemon is macOS-only")
	}
	config := testConfig(filepath.Join(shortTempDir(t), "wake.sock"), filepath.Join(t.TempDir(), "state"))
	config.RequireRoot = true
	config.EUID = func() int { return 501 }
	service, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Serve(context.Background()); !errors.Is(err, ErrRequiresRoot) {
		t.Fatalf("Serve() error = %v, want ErrRequiresRoot", err)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/private/tmp", "hws-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
