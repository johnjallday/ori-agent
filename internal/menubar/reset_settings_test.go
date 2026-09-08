package menubar

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetHostShellMutationCoversActionAndFinalSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	g := &resetstate.WorkGate{}
	shell := NewSettingsManagerWithAdmission(onboarding.NewManager(path), g)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	finish := sync.OnceFunc(func() { close(release) })
	join := sync.OnceFunc(func() {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owned shell action did not finish")
		}
	})
	t.Cleanup(func() { finish(); join() })
	go func() {
		done <- shell.WithMutation(func() error {
			close(started)
			<-release // Models the admitted native action, with no actual OS call.
			return shell.SetPort(9001)
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("owned shell action did not start")
	}
	if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatal("reset ignored active shell action:", err)
	}
	finish()
	join()
	fresh := NewSettingsManagerWithAdmission(onboarding.NewManager(path), g)
	if fresh.GetPort() != 9001 {
		t.Fatal("normal shell action lost its save")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, manager := range []*SettingsManager{shell, fresh} {
		if err := manager.SetPort(9010); !errors.Is(err, resetstate.ErrWorkFenced) {
			t.Fatal("cached shell bypassed fence:", err)
		}
		if err := manager.SetAutoStartEnabled(true); !errors.Is(err, resetstate.ErrWorkFenced) {
			t.Fatal("autostart bypassed fence:", err)
		}
		called := false
		if err := manager.WithMutation(func() error { called = true; return nil }); !errors.Is(err, resetstate.ErrWorkFenced) || called {
			t.Fatal("fenced native action was invoked")
		}
		if manager.GetPort() != 9001 || manager.GetAutoStartEnabled() {
			t.Fatal("fenced mutation changed shell cache")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("fenced shell rewrote app state:", err)
	}
}
