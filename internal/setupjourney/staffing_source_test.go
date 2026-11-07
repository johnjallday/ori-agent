package setupjourney

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type replacingPreparedGrants struct {
	*staffingGrantStub
	profiles store.Store
}

func (g replacingPreparedGrants) PrepareStaffingGrants(name string, skills []string, personal bool) (func() error, error) {
	if err := g.profiles.DeleteAgent(name); err != nil {
		return nil, err
	}
	if err := g.profiles.CreateAgent(name, &store.CreateAgentConfig{SystemPrompt: "replacement profile"}); err != nil {
		return nil, err
	}
	return g.staffingGrantStub.PrepareStaffingGrants(name, skills, personal)
}

func TestStaffRoleOnWorkspace_ReplacementBeforeGrantCannotAcquireGrantsOrBinding(t *testing.T) {
	adapter, workspaces, scope, grants := staffingFixture(t)
	adapter.grants = replacingPreparedGrants{staffingGrantStub: grants, profiles: adapter.profiles}
	fill := RoleFill{RoleID: "project_lead", Name: "New lead", Mode: StaffingModeCreate}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.ProjectWorkspaceID, []RoleFill{fill}); err == nil {
		t.Fatal("replacement acquired new creation's authority")
	}
	profile, exists := adapter.profiles.GetAgent("New lead")
	if !exists || profile.Settings.SystemPrompt != "replacement profile" {
		t.Fatal("rollback deleted the replacement")
	}
	if len(grants.granted["New lead"]) != 0 {
		t.Fatal("replacement acquired the previous definition's grants")
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(project.AgentInstances) != 0 {
		t.Fatal("replacement acquired a binding")
	}
	if _, copied, _ := workspaces.GetWorkspaceAgent(project.ID, "New lead"); copied {
		t.Fatal("replacement was copied")
	}
}

type externalStaffingGrants struct{ StaffingToolGrants }

func TestStaffingRollbackRequiresExactUnsharedCreationAndOwnedGrantRecords(t *testing.T) {
	for _, scenario := range []string{"owned", "shared", "replacement", "unknown_references", "external_grants"} {
		t.Run(scenario, func(t *testing.T) {
			adapter, workspaces, _, _ := staffingFixture(t)
			root := t.TempDir()
			profiles, err := store.NewFileStore(filepath.Join(root, "agents.json"), types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			adapter.profiles = profiles
			owned, err := adapter.createStaffingProfile(t.Context(), "Created helper", &store.CreateAgentConfig{})
			if err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(root, "agents", "Created helper", "skills_state.json")
			if err := os.WriteFile(statePath, []byte(`{"skills":{"project-skill":{"enabled":true}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "shared":
				other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other"})
				other.AgentInstances = []workspace.AgentInstance{{Name: "Created helper"}}
				if err := workspaces.Save(other); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := profiles.DeleteAgent("Created helper"); err != nil {
					t.Fatal(err)
				}
				if err := profiles.CreateAgent("Created helper", &store.CreateAgentConfig{}); err != nil {
					t.Fatal(err)
				}
			case "unknown_references":
				adapter.workspaces = struct{ workspace.Store }{workspaces}
			case "external_grants":
				adapter.grants = externalStaffingGrants{StaffingToolGrants: adapter.grants}
			}
			adapter.rollbackStaffingProfiles(t.Context(), []store.StaffingSelection{owned}, map[string][]string{"Created helper": {"project-skill"}})
			_, exists := profiles.GetAgent("Created helper")
			if exists != (scenario != "owned") {
				t.Fatalf("profile retained=%v for %s", exists, scenario)
			}
			if scenario == "owned" {
				if _, err := os.Stat(statePath); !os.IsNotExist(err) {
					t.Fatal("owned grant records survived deleted definition")
				}
			}
		})
	}
}

func boundSourceWitness(t *testing.T, adapter *AssistantStaffingAdapter) string {
	t.Helper()
	for _, profile := range store.ReadStaffingSnapshot(t.Context(), adapter.profiles).Profiles {
		if profile.Name == existingAgentName {
			return profile.Witness
		}
	}
	t.Fatal("canonical fixture profile missing")
	return ""
}

func TestStaffRoleOnWorkspace_ReviewedWitnessRefusesChangedOrIncompatibleSource(t *testing.T) {
	for _, change := range []string{"edit", "replacement", "wrong_witness", "wrong_scope", "create_action"} {
		t.Run(change, func(t *testing.T) {
			adapter, workspaces, scope, _ := staffingFixture(t)
			saveExistingAgent(t, adapter)
			fill := RoleFill{RoleID: "project_reviewer", Name: existingAgentName, Mode: StaffingModeBind, SourceWitness: boundSourceWitness(t, adapter)}
			switch change {
			case "edit":
				if _, err := store.MutateDefinition(adapter.profiles, existingAgentName, func(a *agent.Agent) error { a.Settings.SystemPrompt = "changed after review"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := adapter.profiles.DeleteAgent(existingAgentName); err != nil {
					t.Fatal(err)
				}
				saveExistingAgent(t, adapter)
			case "wrong_witness":
				fill.SourceWitness = "foreign-source"
			case "wrong_scope":
				fill.RoleID = "home_guide"
			case "create_action":
				fill.Mode = StaffingModeCreate
				fill.Name = "Must not create"
			}
			if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.ProjectWorkspaceID, []RoleFill{fill}); err == nil {
				t.Fatal("changed source/action acquired binding")
			}
			project, _ := workspaces.Get(scope.ProjectWorkspaceID)
			if len(project.AgentInstances) != 0 {
				t.Fatal("rejected intent attached an agent")
			}
			if _, found, _ := workspaces.GetWorkspaceAgent(project.ID, existingAgentName); found {
				t.Fatal("rejected source was copied")
			}
			if _, found := adapter.profiles.GetAgent("Must not create"); found {
				t.Fatal("witness-bearing create acquired a definition")
			}
		})
	}
}

type forbidSnapshotNameLookup struct{ store.Store }

func (forbidSnapshotNameLookup) GetAgent(string) (*agent.Agent, bool) {
	panic("snapshot decorator re-entered source store under binding fence")
}

func TestStaffRoleOnWorkspace_FencedBindingBypassesImplicitSnapshotLookup(t *testing.T) {
	adapter, workspaces, scope, grants := staffingFixture(t)
	saveExistingAgent(t, adapter)
	adapter.workspaces = workspace.NewAgentSnapshotStore(workspaces, forbidSnapshotNameLookup{Store: adapter.profiles})
	fill := RoleFill{RoleID: "project_reviewer", Name: existingAgentName, Mode: StaffingModeBind, SourceWitness: boundSourceWitness(t, adapter)}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.ProjectWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal(err)
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(project.AgentInstances) != 1 || project.AgentInstances[0].RoleSource != workspace.RoleSourceAssigned {
		t.Fatal("reviewed binding absent")
	}
	profile, found, err := workspaces.GetWorkspaceAgent(project.ID, existingAgentName)
	if err != nil || !found || profile.Settings.SystemPrompt != "the user's own prompt" {
		t.Fatal("did not save the exact fenced definition")
	}
	if grants.granted[existingAgentName] != nil {
		t.Fatal("bind granted tools")
	}
}

type alterStaffingTarget struct{ workspace.Store }

func (s alterStaffingTarget) Update(id string, fn func(*workspace.Workspace) error) error {
	return s.Store.Update(id, func(current *workspace.Workspace) error {
		current.OwnerUserID = "foreign"
		return fn(current)
	})
}

func TestStaffRoleOnWorkspace_RechecksTargetOwnershipBeforeCopyOrBinding(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	saveExistingAgent(t, adapter)
	adapter.workspaces = alterStaffingTarget{Store: workspaces}
	fill := RoleFill{RoleID: "project_reviewer", Name: existingAgentName, Mode: StaffingModeBind, SourceWitness: boundSourceWitness(t, adapter)}
	if err := adapter.StaffRoleOnWorkspace(context.Background(), scope.ProjectWorkspaceID, []RoleFill{fill}); err == nil {
		t.Fatal("ownership change was ignored")
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(project.AgentInstances) != 0 {
		t.Fatal("foreign target acquired binding")
	}
	if _, found, _ := workspaces.GetWorkspaceAgent(project.ID, existingAgentName); found {
		t.Fatal("foreign target acquired profile copy")
	}
}
