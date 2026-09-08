//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/menubar"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetShellHostRefusesBeforeConstruction(t *testing.T) {
	for _, mode := range []string{"fenced", "cwd", "data", "receipt"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("ORI_DATA_DIR", root)
			lease, err := resetstate.Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := lease.Close(); err != nil {
					t.Error(err)
				}
			})
			switch mode {
			case "fenced":
				if err := lease.WorkGate().TryFence(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "cwd":
				t.Chdir(t.TempDir())
			case "data":
				t.Setenv("ORI_DATA_DIR", t.TempDir())
			case "receipt":
				if err := lease.Replace(resetstate.PolicyRecord, []byte(`{"future":true}`)); err != nil {
					t.Fatal(err)
				}
			}
			old := newShellOnboarding
			t.Cleanup(func() { newShellOnboarding = old })
			called := false
			newShellOnboarding = func(string) *onboarding.Manager { called = true; return nil }
			if manager, err := initializeMenubarSettings(lease); err == nil || manager != nil || called {
				t.Fatal("shell constructor crossed blocked host:", err)
			}
			if lease.WorkGate().Snapshot().Active != 0 {
				t.Fatal("shell refusal leaked admission")
			}
		})
	}
}

func TestResetMenuRefusesBeforePortInspectionAndNativeDialog(t *testing.T) {
	g := &resetstate.WorkGate{}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	settings := menubar.NewSettingsManagerWithAdmission(nil, g)
	controller := menubar.NewController(0)
	oldPort, oldDialog := menuPortCheck, menuInputDialog
	t.Cleanup(func() { menuPortCheck, menuInputDialog = oldPort, oldDialog })
	called := false
	// Even a regression must stop before the real port/AppleScript commands.
	menuPortCheck = func(int) bool { called = true; return false }
	menuInputDialog = func(string, string, string) (string, error) { called = true; return "", nil }
	if err := startMenuServer(controller, settings); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	handlePortConfigurationSystray(controller, settings, nil)
	if called {
		t.Fatal("fenced menu reached native preflight/dialog")
	}
}

func TestResetMenuRootMismatchRefusesBeforePortTakeover(t *testing.T) {
	root := t.TempDir()
	lease, err := resetstate.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	t.Chdir(t.TempDir()) // Deliberately unlike the leased installation.
	t.Setenv("ORI_DATA_DIR", root)
	settings := menubar.NewSettingsManagerWithAdmission(nil, lease.WorkGate())
	controller := menubar.NewControllerWithResetLease(0, lease)
	old := menuPortCheck
	t.Cleanup(func() { menuPortCheck = old })
	called := false
	menuPortCheck = func(int) bool { called = true; return false }
	if err := startMenuServer(controller, settings); !errors.Is(err, resetstate.ErrRuntimeRootMismatch) || called {
		t.Fatal("wrong-root menu reached port preflight:", err)
	}
	if _, err := os.Stat(filepath.Join(root, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("unexpected store construction:", err)
	}
}
