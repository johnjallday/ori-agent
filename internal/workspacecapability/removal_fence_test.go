package workspacecapability

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type scopedRemovalFixture struct {
	*recordingRuntime
	replace func() error
	checked bool
}

func (r *scopedRemovalFixture) OnCapabilityRemove(string) error { return r.replace() }
func (r *scopedRemovalFixture) ValidateCapabilityRemoval(string) error {
	r.checked = true
	return errors.New("removal scope changed")
}
func TestRemovalFinalFencePreservesReinstallationAfterDrainedCallback(t *testing.T) {
	service, base, store := removalFixture(t)
	runtime := &scopedRemovalFixture{recordingRuntime: base}
	runtime.replace = func() error {
		return store.Update("ws-1", func(current *workspace.Workspace) error {
			current.InstalledCapabilities[0].InstalledAt = current.InstalledCapabilities[0].InstalledAt.Add(time.Second)
			return nil
		})
	}
	if err := service.registry.BindRuntime(workspace.CapabilityFileJanitor, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{}); err == nil {
		t.Fatal("old callback removed a new installation")
	}
	current, err := store.Get("ws-1")
	if err != nil || !runtime.checked || !current.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("final fence was not applied", err)
	}
}
