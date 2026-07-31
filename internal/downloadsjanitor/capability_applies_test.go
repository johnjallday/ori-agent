package downloadsjanitor

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestAppliesTo_InstalledCapabilityAlone covers the gap a live smoke test
// found: installing File Janitor into an ordinary workspace succeeded, and then
// no surface appeared anywhere, because AppliesTo only knew about template
// provenance, finished settings, and pending directory requirements — none of
// which an in-place install produces.
//
// From the user's side that is identical to the install having failed. Every
// unit test passed, because each one started from a workspace that already
// carried one of the older signals.
func TestAppliesTo_InstalledCapabilityAlone(t *testing.T) {
	service, workspaces := newTestService(t)

	ws := workspaces.workspaces["ws-1"]
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID:          workspace.CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}

	if !service.AppliesTo("ws-1") {
		t.Fatal("a workspace with the capability installed must show File Janitor")
	}

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Applies {
		t.Fatal("Status.Applies must agree with AppliesTo; every UI surface mounts on it")
	}
	// Installing grants nothing on its own: the folder is still unchosen, and
	// setup is still the only thing that can choose it (FR-20, FR-23).
	if status.Settings.IsSetUp() {
		t.Error("installing must not configure a folder")
	}
	if status.Readiness.State != ReadinessSetupRequired {
		t.Errorf("readiness = %q, want setup_required", status.Readiness.State)
	}
}

// A workspace with no install, no template, and no settings stays clean:
// AppliesTo is what keeps File Janitor's surfaces out of workspaces that never
// asked for it.
func TestAppliesTo_UninvolvedWorkspace(t *testing.T) {
	service, _ := newTestService(t)
	if service.AppliesTo("ws-2") {
		t.Fatal("File Janitor must not appear in a workspace that never installed it")
	}
}
