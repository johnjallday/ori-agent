package workspacecapability

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// associationUpdateStore models an in-flight roster change without the fixture
// store's JSON reader normalizing duplicate names before the guard sees them.
type associationUpdateStore struct {
	WorkspaceStore
	before func(*workspace.Workspace)
}

func (s associationUpdateStore) Update(id string, fn func(*workspace.Workspace) error) error {
	return s.WorkspaceStore.Update(id, func(w *workspace.Workspace) error {
		s.before(w)
		return fn(w)
	})
}

func TestFolderCompanion_GuardsTheAssociationInsideUpdate(t *testing.T) {
	for _, scenario := range []string{"fresh", "repeat", "missing", "ambiguous name", "ambiguous id", "changed lead", "replaced attachment", "other companion", "ambiguous companion", "save failure"} {
		t.Run(scenario, func(t *testing.T) {
			ws := installedWorkspaceFixture()
			ws.AgentInstances = []workspace.AgentInstance{{ID: "hired", Name: "Downloads Manager", NodeID: "hired", EntryPoint: true}}
			s := newMemStore(ws)
			p := &fakeProvisioner{}
			svc := companionService(t, s, p, Status{})
			// Model the change after the host found the attachment, before the
			// service commits. The service must not trust that earlier lookup.
			if err := s.Update(ws.ID, func(w *workspace.Workspace) error {
				switch scenario {
				case "missing":
					w.AgentInstances = nil
				case "ambiguous id":
					w.AgentInstances = append(w.AgentInstances, workspace.AgentInstance{ID: "hired", Name: "Other"})
				case "changed lead":
					w.AgentInstances = append(w.AgentInstances, workspace.AgentInstance{ID: "other", Name: "Other"})
					return w.SetEntryAgentName("Other")
				case "replaced attachment":
					w.AgentInstances[0].ID = "new"
				case "repeat":
					w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "hired", Shared: true})
				case "other companion", "ambiguous companion":
					w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "other"})
					if scenario == "ambiguous companion" {
						w.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "hired"})
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := s.Get(ws.ID)
			if scenario == "ambiguous name" {
				svc.store = associationUpdateStore{WorkspaceStore: s, before: func(w *workspace.Workspace) {
					w.AgentInstances = append(w.AgentInstances, workspace.AgentInstance{ID: "second", Name: "Downloads Manager", NodeID: "second"})
				}}
			}
			if scenario == "save failure" {
				s.saveErr = errors.New("disk full")
			}
			result, err := svc.AssociateCompanion(ws.ID, workspace.CapabilityFileJanitor, "hired", CompanionAssociationOptions{ExpectedLead: "Downloads Manager"})
			wantSuccess := scenario == "fresh" || scenario == "repeat"
			if (err == nil) != wantSuccess || p.calls != 0 {
				t.Fatal("wrong result or invoked provisioner", result, err, p.calls)
			}
			after, _ := s.Get(ws.ID)
			if !reflect.DeepEqual(before.AgentInstances, after.AgentInstances) || !reflect.DeepEqual(before.MCPBindings, after.MCPBindings) {
				t.Fatal("association changed roster or tool grants")
			}
			if wantSuccess {
				curator, ok := after.FileCurator()
				if !ok || curator.ID != "hired" || result.AlreadyPresent != (scenario == "repeat") {
					t.Fatal("wrong Curator", curator, result)
				}
			} else if !reflect.DeepEqual(before.InstalledCapabilities, after.InstalledCapabilities) {
				t.Fatal("failed association changed install")
			}
		})
	}
}

func TestFolderCompanion_ReusedAgentIsScopedToThisWorkspace(t *testing.T) {
	ws := installedWorkspaceFixture()
	ws.AgentInstances = []workspace.AgentInstance{{ID: "here", Name: "Shared agent", EntryPoint: true}}
	other := cloneWorkspace(ws)
	other.ID = "other"
	other.AgentInstances[0].ID = "elsewhere"
	other.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "elsewhere"})
	s := newMemStore(ws, other)
	svc := newTestService(t, s)
	before, _ := s.Get(other.ID)
	if _, err := svc.AssociateCompanion(ws.ID, workspace.CapabilityFileJanitor, "here", CompanionAssociationOptions{ExpectedLead: "Shared agent"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(other.ID)
	if !reflect.DeepEqual(got, before) {
		t.Fatal("changed the agent's other workspace")
	}
	plan, err := svc.RemovalPlan(ws.ID, workspace.CapabilityFileJanitor)
	if err != nil || plan.Companion == nil || plan.Companion.Removable || !strings.Contains(plan.Companion.Reason, "leaves it alone") {
		t.Fatal("removal acquired ownership", plan, err)
	}
}
