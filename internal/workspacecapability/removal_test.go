package workspacecapability

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// recordingRuntime observes the removal sequence. The ORDER it records is the
// thing under test: access must not be released while automation is still
// running against it.
type recordingRuntime struct {
	calls        []string
	stopErr      error
	removeErr    error
	facts        RemovalFacts
	statusResult Status
}

func (r *recordingRuntime) CapabilityStatus(string) (Status, error) { return r.statusResult, nil }

func (r *recordingRuntime) StopCapabilityAutomation(string) error {
	r.calls = append(r.calls, "stop-automation")
	return r.stopErr
}

func (r *recordingRuntime) OnCapabilityRemove(string) error {
	r.calls = append(r.calls, "release-access")
	return r.removeErr
}

func (r *recordingRuntime) DescribeCapabilityRemoval(string) (RemovalFacts, error) {
	return r.facts, nil
}

type recordingCompanions struct {
	created   []string
	removed   []string
	removeErr error
}

func (c *recordingCompanions) EnsureCompanionAgent(_, displayName string) (string, bool, error) {
	c.created = append(c.created, displayName)
	return "agent-instance-1", true, nil
}

func (c *recordingCompanions) RemoveCompanionAgent(_, agentInstanceID string) error {
	if c.removeErr != nil {
		return c.removeErr
	}
	c.removed = append(c.removed, agentInstanceID)
	return nil
}

func removalFixture(t *testing.T, resources ...workspace.CapabilityResource) (*Service, *recordingRuntime, *memStore) {
	t.Helper()
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	runtime := &recordingRuntime{
		facts: RemovalFacts{
			ManagedFolder: "Inbox",
			Automation:    []string{"Watching this folder for new files."},
			RetainedAudit: []string{"The history of everything File Janitor filed."},
		},
	}
	if err := registry.BindRuntime(workspace.CapabilityFileJanitor, runtime); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	ws := &workspace.Workspace{ID: "ws-1", Name: "Files"}
	record := workspace.InstalledCapability{
		ID:             workspace.CapabilityFileJanitor,
		Version:        1,
		InstalledAt:    time.Now(),
		Source:         workspace.InstallSourceInPlace,
		OwnedResources: resources,
	}
	if _, err := ws.AddInstalledCapability(record); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	store := newMemStore(ws)
	return NewService(registry, store), runtime, store
}

// The order is the safety property. Releasing a folder while a watcher still
// points at it is the one sequence that must never happen (FR-26).
func TestRemove_StopsAutomationBeforeReleasingAccess(t *testing.T) {
	service, runtime, store := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceDirectoryReference, ID: "ref-1"})

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected removal to succeed")
	}
	if len(runtime.calls) != 2 || runtime.calls[0] != "stop-automation" || runtime.calls[1] != "release-access" {
		t.Fatalf("call order = %v, want stop-automation then release-access", runtime.calls)
	}
	if store.workspaces["ws-1"].HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Error("the install record must be gone once removal succeeds")
	}
}

// The record is dropped LAST. While it is still there the capability reports
// itself installed and unhealthy — visible and repairable. Dropping it first
// would leave live watchers behind with nothing recording that they exist.
func TestRemove_KeepsTheRecordWhenAutomationCannotBeStopped(t *testing.T) {
	service, runtime, store := removalFixture(t)
	runtime.stopErr = errors.New("watcher is busy")

	_, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err == nil {
		t.Fatal("expected removal to refuse when automation cannot be stopped")
	}
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeRemovalIncomplete {
		t.Fatalf("error = %v, want CodeRemovalIncomplete", err)
	}
	if !store.workspaces["ws-1"].HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Error("the record must survive a failed teardown so the retry can finish")
	}
	// Access was never touched, because the watcher is still running.
	for _, call := range runtime.calls {
		if call == "release-access" {
			t.Fatal("access must not be released while automation is still running")
		}
	}
}

func TestRemove_KeepsTheRecordWhenAccessCannotBeReleased(t *testing.T) {
	service, runtime, store := removalFixture(t)
	runtime.removeErr = errors.New("folder is locked")

	_, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err == nil {
		t.Fatal("expected removal to refuse")
	}
	if !store.workspaces["ws-1"].HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Error("a partial teardown must stay visible as an installed, unhealthy capability")
	}
}

// A retry after a partial failure has to be able to finish, and removing
// something already gone is success rather than an error (FR-15).
func TestRemove_IsIdempotent(t *testing.T) {
	service, _, _ := removalFixture(t)

	if _, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{}); err != nil {
		t.Fatalf("first removal: %v", err)
	}
	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err != nil {
		t.Fatalf("second removal must not error: %v", err)
	}
	if !result.AlreadyRemoved {
		t.Error("a repeated removal should report already_removed")
	}
}

// A resource File Janitor adopted rather than created belongs to whatever else
// is using it. Deleting it would revoke a grant on something else's behalf, and
// the user would experience that as an unrelated feature breaking (FR-27).
func TestRemove_LeavesSharedResourcesToTheirOtherOwners(t *testing.T) {
	service, _, _ := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceDirectoryReference, ID: "ref-shared", Shared: true},
		workspace.CapabilityResource{Kind: workspace.ResourceMCPBinding, ID: "binding-own"})

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(result.KeptShared) != 1 || result.KeptShared[0].ID != "ref-shared" {
		t.Errorf("kept shared = %v, want the shared reference", result.KeptShared)
	}
	if len(result.Released) != 1 || result.Released[0].ID != "binding-own" {
		t.Errorf("released = %v, want only the exclusively-owned binding", result.Released)
	}
}

// Uninstalling a capability is not consent to delete an agent.
func TestRemove_LeavesTheCompanionAloneByDefault(t *testing.T) {
	service, _, _ := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "agent-1"})
	companions := &recordingCompanions{}
	service.SetCompanionProvisioner(companions)

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.CompanionRemoved {
		t.Error("the companion must not be removed unless separately confirmed")
	}
	if len(companions.removed) != 0 {
		t.Errorf("removed = %v, want nothing", companions.removed)
	}
}

func TestRemove_RemovesTheCompanionWhenSeparatelyConfirmed(t *testing.T) {
	service, _, _ := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "agent-1"})
	companions := &recordingCompanions{}
	service.SetCompanionProvisioner(companions)

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor,
		RemoveOptions{RemoveCompanion: true})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.CompanionRemoved {
		t.Fatal("expected the confirmed companion removal")
	}
	if len(companions.removed) != 1 || companions.removed[0] != "agent-1" {
		t.Errorf("removed = %v, want [agent-1]", companions.removed)
	}
}

// An agent File Janitor adopted rather than created existed before it and will
// outlive it. Even an explicit "remove the companion" must not delete it —
// the user is confirming removal of the capability's own agent, not of one
// that merely shares its name or role.
func TestRemove_NeverRemovesAnAdoptedCompanion(t *testing.T) {
	service, _, _ := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "agent-preexisting", Shared: true})
	companions := &recordingCompanions{}
	service.SetCompanionProvisioner(companions)

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor,
		RemoveOptions{RemoveCompanion: true})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.CompanionRemoved {
		t.Error("an adopted agent must survive removal even when removal was confirmed")
	}
	if len(companions.removed) != 0 {
		t.Errorf("removed = %v, want nothing", companions.removed)
	}
}

// The confirmation must state what removal does to THIS workspace. Generic copy
// cannot name the folder, and a user who cannot see which folder is losing
// access cannot evaluate the decision (FR-24, FR-25).
func TestRemovalPlan_DescribesThisWorkspace(t *testing.T) {
	service, _, store := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceDirectoryReference, ID: "ref-1"},
		workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "agent-1"})

	summary, err := service.RemovalPlan("ws-1", workspace.CapabilityFileJanitor)
	if err != nil {
		t.Fatalf("RemovalPlan: %v", err)
	}
	if !summary.Installed {
		t.Fatal("expected an installed capability")
	}
	if summary.ManagedFolder != "Inbox" {
		t.Errorf("managed folder = %q, want the folder this workspace manages", summary.ManagedFolder)
	}
	if len(summary.StopsAutomation) == 0 {
		t.Error("the confirmation must say what stops")
	}
	if len(summary.RetainedAudit) == 0 {
		t.Error("the confirmation must say what is kept")
	}
	if summary.MovesFiles {
		t.Error("removal must never move files, and must say so")
	}
	if summary.Companion == nil || !summary.Companion.Removable {
		t.Error("a capability-created companion should be offered for removal")
	}

	// A dry run changes nothing.
	if !store.workspaces["ws-1"].HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("computing the summary must not remove anything")
	}
}

func TestRemovalPlan_MarksAnAdoptedCompanionAsNotRemovable(t *testing.T) {
	service, _, _ := removalFixture(t,
		workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "agent-1", Shared: true})

	summary, err := service.RemovalPlan("ws-1", workspace.CapabilityFileJanitor)
	if err != nil {
		t.Fatalf("RemovalPlan: %v", err)
	}
	if summary.Companion == nil {
		t.Fatal("expected the companion to be described")
	}
	if summary.Companion.Removable {
		t.Error("an adopted agent must not be offered for deletion")
	}
	if summary.Companion.Reason == "" {
		t.Error("the confirmation should say why it is being left alone")
	}
}

// A capability this build cannot resolve is exactly one a user should be able
// to get rid of. Removal must not depend on a runtime being bound.
func TestRemove_WorksWithoutABoundRuntime(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-1"}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(),
		Source: workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	store := newMemStore(ws)
	service := NewService(registry, store)

	result, err := service.Remove("ws-1", workspace.CapabilityFileJanitor, RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Removed {
		t.Error("a capability with no bound runtime must still be removable")
	}
}

func TestRemove_RejectsAnUnknownWorkspace(t *testing.T) {
	service, _, _ := removalFixture(t)

	_, err := service.Remove("ws-missing", workspace.CapabilityFileJanitor, RemoveOptions{})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeWorkspaceMissing {
		t.Fatalf("error = %v, want CodeWorkspaceMissing", err)
	}
}
