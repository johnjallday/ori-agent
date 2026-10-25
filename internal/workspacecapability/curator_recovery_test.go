package workspacecapability

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCuratorMigrationOnlyRepairsUnambiguousRoleEvidence(t *testing.T) {
	proven := installedWorkspaceFixture()
	proven.InstalledCapabilities[0].Source = workspace.InstallSourceBlueprint
	proven.TemplateProvenance = &workspace.TemplateProvenance{TemplateID: "file-janitor", Builtin: true}
	proven.AgentInstances = append(proven.AgentInstances, workspace.AgentInstance{ID: "helper", Name: "My renamed helper", NodeID: "helper-node", RoleID: "file-curator", RoleSource: workspace.RoleSourceAssigned})
	unproven := cloneWorkspace(proven)
	unproven.ID = "ws-2"
	unproven.AgentInstances[1].RoleID = ""
	unproven.AgentInstances[1].Name = "File Curator"
	s := newMigrationStore(proven, unproven)
	m := newTestMigrator(t, s, nil)
	m.Run()
	firstSaves := s.saves
	m.Run()
	if s.saves != firstSaves || firstSaves != 1 {
		t.Fatalf("not idempotent: %d -> %d", firstSaves, s.saves)
	}
	got, _ := s.Get(proven.ID)
	if instance, ok := got.FileCurator(); !ok || instance.ID != "helper" {
		t.Fatalf("repair = %+v %v", instance, ok)
	}
	other, _ := s.Get(unproven.ID)
	if _, ok := other.FileCurator(); ok {
		t.Fatal("name-only roster was guessed")
	}
}

func TestExplicitCuratorAssociationRecoversWithoutMutatingRoster(t *testing.T) {
	ws := installedWorkspaceFixture()
	ws.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "gone"})
	ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{ID: "chosen", Name: "Saved helper", NodeID: "helper-node"})
	s := newMemStore(ws)
	svc := newTestService(t, s)
	if _, err := svc.AssociateCompanion(ws.ID, workspace.CapabilityFileJanitor, "other-workspace-agent"); err == nil {
		t.Fatal("accepted absent attachment")
	}
	result, err := svc.AssociateCompanion(ws.ID, workspace.CapabilityFileJanitor, "chosen")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ws.ID)
	if result.AgentInstanceID != "chosen" || len(got.AgentInstances) != len(ws.AgentInstances) || got.EntryAgentName() != ws.EntryAgentName() {
		t.Fatal("association changed roster")
	}
	record, _ := got.GetInstalledCapability(workspace.CapabilityFileJanitor)
	resources := record.ResourcesOfKind(workspace.ResourceCompanionAgent)
	if len(resources) != 1 || !resources[0].Shared {
		t.Fatalf("recovery acquired ownership: %+v", resources)
	}
}

func TestCompanionAssociationSaveFailureIsNotReportedAsSuccess(t *testing.T) {
	s := newMemStore(installedWorkspaceFixture())
	s.saveErr = errors.New("disk full")
	svc := companionService(t, s, &fakeProvisioner{created: true}, Status{})
	if _, err := svc.AddCompanion("ws-1", workspace.CapabilityFileJanitor); err == nil {
		t.Fatal("failed association reported success")
	}
}

func TestAddCuratorRejectsMultipleLiveAssociations(t *testing.T) {
	w := installedWorkspaceFixture()
	for _, id := range []string{"one", "two"} {
		w.AgentInstances = append(w.AgentInstances, workspace.AgentInstance{ID: id, Name: id, NodeID: id + "-node"})
		w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: id})
	}
	p := &fakeProvisioner{}
	svc := companionService(t, newMemStore(w), p, Status{})
	if _, err := svc.AddCompanion(w.ID, workspace.CapabilityFileJanitor); err == nil || p.calls != 0 {
		t.Fatal("ambiguous association silently chose or created an agent")
	}
}
