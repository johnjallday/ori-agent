//go:build darwin

package main

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func TestMenubarResetRelaunchChecksExactOperationAndFixedPort(t *testing.T) {
	for _, args := range [][]string{nil, {"valid"}, {"bad/path", "8765"}, {"valid", "0"}, {"valid", "65536"}, {"valid", "8765", "extra"}} {
		if _, _, err := parseNativeResetArgs(args); err == nil {
			t.Errorf("accepted invalid native relaunch arguments: %q", args)
		}
	}
	for _, want := range []int{8765, 17865} {
		if id, port, err := parseNativeResetArgs([]string{"reviewed-reset", strconv.Itoa(want)}); id != "reviewed-reset" || port != want || err != nil {
			t.Fatalf("lost the reviewed relaunch identity: %q %d %v", id, port, err)
		}
	}
	if launch, err := identifyNativeResetLaunch(""); err == nil || launch.unchanged() {
		t.Fatal("a raw executable advertised a native relaunch path")
	}
}

func TestMenubarResetRelaunchNeverTakesAnotherListener(t *testing.T) {
	existing, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = existing.Close() }()
	port := existing.Addr().(*net.TCPAddr).Port
	if listener, err := reserveNativeResetPort(port); err == nil {
		_ = listener.Close()
		t.Fatal("native reset reserved a port already owned by another listener")
	}
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", existing.Addr().String())
	if err != nil {
		t.Fatal("native reset affected the existing listener:", err)
	}
	_ = conn.Close()
}

func TestMenubarResetFirstRunKeepsPortAndLoginPreference(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PORT", "")
	if p := menubarRecoveryPort(root); p != 8765 {
		t.Fatalf("default recovery port = %d", p)
	}
	path := filepath.Join(root, "app_state.json")
	setup := onboarding.NewManager(path)
	if err := setup.SetMenuBarPort(18765); err != nil {
		t.Fatal(err)
	}
	if err := setup.SetMenuBarAutoStart(true); err != nil {
		t.Fatal(err)
	}
	if err := setup.CompleteOnboarding(); err != nil {
		t.Fatal(err)
	}
	if err := setup.ResetFirstRunVerified(); err != nil {
		t.Fatal(err)
	}
	fresh, err := onboarding.OpenForReset(path)
	if err != nil || !fresh.IsCanonicalFirstRun() || !fresh.GetMenuBarAutoStart() || fresh.GetMenuBarPort() != 18765 || menubarRecoveryPort(root) != 18765 {
		t.Fatal("reset did not preserve the host launch preference and custom port", err)
	}
	// The recovery listener's read must neither follow a symlink nor modify state.
	other := filepath.Join(root, "moved")
	if err := os.Rename(path, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if p := menubarRecoveryPort(root); p != 8765 {
		t.Fatalf("unsafe recovery port = %d", p)
	}
	after, err := os.ReadFile(other)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("recovery port probe modified state", err)
	}
}
