package workspacecapability

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeProvisioner records companion creation and can simulate reusing an agent
// that already exists.
type fakeProvisioner struct {
	calls       int
	instanceID  string
	created     bool
	err         error
	lastName    string
	workspaceID string
}

func (f *fakeProvisioner) EnsureCompanionAgent(workspaceID, displayName string) (string, bool, error) {
	f.calls++
	f.lastName = displayName
	f.workspaceID = workspaceID
	if f.err != nil {
		return "", false, f.err
	}
	id := f.instanceID
	if id == "" {
		id = "agent-instance-1"
	}
	return id, f.created, nil
}

func installedWorkspaceFixture() *workspace.Workspace {
	ws := testWorkspace()
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID:          workspace.CapabilityFileJanitor,
		Version:     FileJanitorDefinitionVersion,
		InstalledAt: time.Now(),
		Source:      workspace.InstallSourceInPlace,
	}); err != nil {
		panic(err)
	}
	return ws
}

func companionService(t *testing.T, store WorkspaceStore, provisioner CompanionProvisioner, status Status) *Service {
	t.Helper()
	svc := NewService(mustBuiltinRegistry(t), store)
	svc.SetCompanionProvisioner(provisioner)
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, &stubRuntime{status: status}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}
	return svc
}

func TestAddCompanion_CreatesOneAndRecordsTheAssociation(t *testing.T) {
	store := newMemStore(installedWorkspaceFixture())
	provisioner := &fakeProvisioner{created: true}
	svc := companionService(t, store, provisioner, Status{State: StatusSetupNeeded})

	result, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
	if err != nil {
		t.Fatalf("AddCompanion: %v", err)
	}
	if result.AlreadyPresent {
		t.Fatal("a first companion must not report already-present")
	}
	if result.AgentInstanceID != "agent-instance-1" {
		t.Fatalf("instance = %q", result.AgentInstanceID)
	}

	ws, _ := store.Get("ws-1")
	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	exclusive, recorded := record.Owns(workspace.ResourceCompanionAgent, "agent-instance-1")
	if !recorded {
		t.Fatalf("the companion association was not recorded: %+v", record.OwnedResources)
	}
	if !exclusive {
		t.Fatal("an agent the capability created should be exclusively owned")
	}
}

// TestAddCompanion_IsIdempotentThroughAssociationNotName is FR-39 and the
// reason ownership is keyed on IDs (PRD §9.5).
//
// The user renames their Curator. A name-matching implementation would fail to
// find it and create a second one; the association still resolves.
func TestAddCompanion_IsIdempotentThroughAssociationNotName(t *testing.T) {
	store := newMemStore(installedWorkspaceFixture())
	provisioner := &fakeProvisioner{created: true}
	svc := companionService(t, store, provisioner, Status{State: StatusWatching, FolderDisplayName: "Downloads"})

	if _, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor); err != nil {
		t.Fatalf("first add: %v", err)
	}

	// The user renames the agent to something of their own.
	if err := store.Update("ws-1", func(w *workspace.Workspace) error {
		w.AgentInstances = append(w.AgentInstances, workspace.AgentInstance{
			ID: "agent-instance-1", Name: "Tidy Helper", NodeID: "node-1",
		})
		return nil
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	result, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !result.AlreadyPresent {
		t.Fatal("the renamed companion was not recognized; a second one would have been created")
	}
	if result.DisplayName != "Tidy Helper" {
		t.Fatalf("display name = %q, want the agent's current name", result.DisplayName)
	}
	if provisioner.calls != 1 {
		t.Fatalf("provisioner called %d times; the companion already existed", provisioner.calls)
	}

	ws, _ := store.Get("ws-1")
	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if got := len(record.ResourcesOfKind(workspace.ResourceCompanionAgent)); got != 1 {
		t.Fatalf("expected one companion association, got %d", got)
	}
}

// TestAddCompanion_AdoptedAgentIsRecordedAsShared covers the other direction of
// the same rule: an agent the capability did not create must survive an
// uninstall, so it is never marked exclusively owned.
func TestAddCompanion_AdoptedAgentIsRecordedAsShared(t *testing.T) {
	store := newMemStore(installedWorkspaceFixture())
	// created == false: the provisioner reused an agent that already existed.
	provisioner := &fakeProvisioner{instanceID: "pre-existing-agent", created: false}
	svc := companionService(t, store, provisioner, Status{State: StatusSetupNeeded})

	if _, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor); err != nil {
		t.Fatalf("AddCompanion: %v", err)
	}

	ws, _ := store.Get("ws-1")
	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	exclusive, recorded := record.Owns(workspace.ResourceCompanionAgent, "pre-existing-agent")
	if !recorded {
		t.Fatal("the adopted agent was not associated")
	}
	if exclusive {
		t.Fatal("an agent the capability did not create must be recorded as shared")
	}
}

// TestAddCompanion_NamesTheCuratorAfterTheManagedFolder is FR-40.
func TestAddCompanion_NamesTheCuratorAfterTheManagedFolder(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{"configured folder", Status{State: StatusWatching, FolderDisplayName: "Downloads"}, "Downloads Curator"},
		{"another folder", Status{State: StatusWatching, FolderDisplayName: "Scans"}, "Scans Curator"},
		{"before setup", Status{State: StatusSetupNeeded}, "File Curator"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore(installedWorkspaceFixture())
			provisioner := &fakeProvisioner{created: true}
			svc := companionService(t, store, provisioner, tc.status)

			result, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
			if err != nil {
				t.Fatalf("AddCompanion: %v", err)
			}
			if result.DisplayName != tc.want {
				t.Fatalf("display name = %q, want %q", result.DisplayName, tc.want)
			}
			if provisioner.lastName != tc.want {
				t.Fatalf("provisioner was asked for %q", provisioner.lastName)
			}
		})
	}
}

func TestAddCompanion_RequiresAnInstalledCapability(t *testing.T) {
	store := newMemStore(testWorkspace()) // installed nothing
	svc := companionService(t, store, &fakeProvisioner{}, Status{})

	_, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeCapabilityNotInstalled {
		t.Fatalf("expected capability_not_installed, got %v", err)
	}
}

// TestAddCompanion_FailureLeavesTheCapabilityWorking is FR-37: the Curator is
// optional, so failing to add one must not damage anything.
func TestAddCompanion_FailureLeavesTheCapabilityWorking(t *testing.T) {
	store := newMemStore(installedWorkspaceFixture())
	provisioner := &fakeProvisioner{err: errors.New("agent store unavailable")}
	svc := companionService(t, store, provisioner, Status{State: StatusSetupNeeded})

	_, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeCompanionFailed {
		t.Fatalf("expected companion_failed, got %v", err)
	}
	if !strings.Contains(lifecycleErr.Message, "still works") {
		t.Fatalf("the message should reassure that the capability is unaffected: %q", lifecycleErr.Message)
	}

	ws, _ := store.Get("ws-1")
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("a failed companion add uninstalled the capability")
	}
	record, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if len(record.ResourcesOfKind(workspace.ResourceCompanionAgent)) != 0 {
		t.Fatal("a failed add recorded a companion association anyway")
	}
}

func TestAddCompanion_UnavailableWithoutAProvisioner(t *testing.T) {
	store := newMemStore(installedWorkspaceFixture())
	svc := NewService(mustBuiltinRegistry(t), store)

	_, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor)
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeCompanionUnavailable {
		t.Fatalf("expected companion_unavailable, got %v", err)
	}
}
