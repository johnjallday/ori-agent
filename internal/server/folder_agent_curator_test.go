package server

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// The host path is identical for Downloads and an ordinary loose-files folder.
// It installs in place, adopts only the hired lead, and never calls a provisioner.
func TestFolderAgentFileCurator_AssociatesOnlyTheHiredAgent(t *testing.T) {
	for _, folder := range []string{"Downloads", "Loose Files"} {
		for _, scenario := range []string{"fresh", "missing", "ambiguous", "changed lead", "existing companion"} {
			t.Run(folder+"/"+scenario, func(t *testing.T) {
				builder := newBuiltTestBuilder(t)
				name := folder + " Manager"
				if err := builder.st.CreateAgent(name, &store.CreateAgentConfig{Role: types.RoleOrchestrator}); err != nil {
					t.Fatal(err)
				}
				seed := &workspace.Workspace{
					ID: "ws-folder", Name: folder, FolderSlug: "folder", OwnerUserID: "local",
					Status: workspace.StatusActive, SharedData: map[string]any{},
					CreatedAt: time.Now(), UpdatedAt: time.Now(),
					AgentInstances: []workspace.AgentInstance{{ID: "hired", Name: name, NodeID: "hired", EntryPoint: true}},
				}
				switch scenario {
				case "missing":
					seed.AgentInstances = nil
				case "ambiguous":
					seed.AgentInstances = append(seed.AgentInstances, workspace.AgentInstance{ID: "duplicate", Name: name, NodeID: "duplicate"})
				case "changed lead", "existing companion":
					seed.AgentInstances = append(seed.AgentInstances, workspace.AgentInstance{ID: "other", Name: "Other", NodeID: "other"})
					if scenario == "changed lead" {
						if err := seed.SetEntryAgentName("Other"); err != nil {
							t.Fatal(err)
						}
					} else {
						seed.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, Source: workspace.InstallSourceInPlace,
							OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: "other"}}}}
					}
				}
				if err := builder.workspaceStore.Save(seed); err != nil {
					t.Fatal(err)
				}
				before, err := builder.workspaceStore.Get(seed.ID)
				if err != nil {
					t.Fatal(err)
				}
				beforeAgents := builder.st.ListAgents()
				beforeWorkspaces, err := builder.workspaceStore.List()
				if err != nil {
					t.Fatal(err)
				}
				states, progress := map[string]string{}, map[string]string{}
				set := func(kind, state, text string) { states[kind], progress[kind] = state, text }
				for run := 0; run < 2; run++ {
					err := builder.addFolderAgentFileCurator(folderagent.Record{WorkspaceID: seed.ID, AgentName: name}, set)
					if (err == nil) != (scenario == "fresh") {
						t.Fatalf("run %d: %v", run, err)
					}
				}
				ws, err := builder.workspaceStore.Get(seed.ID)
				if err != nil {
					t.Fatal(err)
				}
				record, ok := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
				if !ok || record.Source != workspace.InstallSourceInPlace || states[folderagent.FolderPlanCapability] != personalassistant.FolderLineDone {
					t.Fatal("File Janitor must remain installed", record, states)
				}
				if !reflect.DeepEqual(ws.AgentInstances, before.AgentInstances) || !reflect.DeepEqual(ws.MCPBindings, before.MCPBindings) || !reflect.DeepEqual(ws.DirectoryReferences, before.DirectoryReferences) {
					t.Fatal("association changed the roster or access")
				}
				resources := record.ResourcesOfKind(workspace.ResourceCompanionAgent)
				if scenario == "fresh" {
					if len(resources) != 1 || resources[0].ID != "hired" || !resources[0].Shared || states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineDone {
						t.Fatal("hired agent was not adopted as shared", resources, states)
					}
					guidance := builder.fileJanitorService.CuratorGuidance(seed.ID)
					if !guidance.Available || guidance.AgentInstanceID != "hired" || guidance.AgentName != name {
						t.Fatal("Settings did not name the hired agent", guidance)
					}
					if _, err := builder.workspaceCapabilityService.Remove(seed.ID, workspace.CapabilityFileJanitor, workspacecapability.RemoveOptions{RemoveCompanion: true}); err != nil {
						t.Fatal(err)
					}
					ws, err = builder.workspaceStore.Get(seed.ID)
					if err != nil || !reflect.DeepEqual(ws.AgentInstances, before.AgentInstances) {
						t.Fatal("removal detached the hired agent", err)
					}
					if _, present := ws.FileCurator(); present {
						t.Fatal("role survived uninstall")
					}
				} else {
					if states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineFailed || !strings.Contains(progress[personalassistant.FolderPlanAgents], "File Janitor still works without an agent.") {
						t.Fatal("missing plain failure message", states, progress)
					}
					if scenario == "existing companion" {
						if len(resources) != 1 || resources[0].ID != "other" {
							t.Fatal("replaced existing companion", resources)
						}
					} else if len(resources) != 0 {
						t.Fatal("recorded a fallback", resources)
					}
				}
				afterWorkspaces, err := builder.workspaceStore.List()
				if err != nil || len(afterWorkspaces) != len(beforeWorkspaces) || !reflect.DeepEqual(builder.st.ListAgents(), beforeAgents) {
					t.Fatal("created a workspace or agent", err)
				}
			})
		}
	}
}
