package setupjourney

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestStaffRoleOnWorkspaceRefusesMissingInitialStageBeforeCreatingProfile(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	if err := workspaces.Update(scope.HomeWorkspaceID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Declaration.Stages = nil
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Aster", Provider: "openai", Model: "gpt-4o-mini", ChoiceDigest: strings.Repeat("a", 64)}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err == nil {
		t.Fatal("uninitializable Home accepted")
	}
	home, _ := workspaces.Get(scope.HomeWorkspaceID)
	if len(adapter.profiles.ListAgents()) != 0 || len(home.GetAgentInstances()) != 0 || len(home.GetAssistantProgramState().StaffingReceipts) != 0 {
		t.Fatal("invalid Home performed staffing effects")
	}
}

func TestStaffRoleOnWorkspaceReviewedOutcomeReplaysWithoutCreationOrModelResolution(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	index := filepath.Join(t.TempDir(), "agents.json")
	profiles, err := store.NewFileStore(index, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	adapter.profiles = profiles
	fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Aster", Provider: "openai", Model: "gpt-4o-mini", ChoiceDigest: strings.Repeat("a", 64)}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal(err)
	}
	home, _ := workspaces.Get(scope.HomeWorkspaceID)
	receipt, found, err := workspace.HomeStaffingReceipt(home.GetAssistantProgramState(), fill.RoleID)
	if err != nil || !found || receipt.ChoiceDigest != fill.ChoiceDigest || receipt.Action != "create" {
		t.Fatal("canonical binding lacks exact outcome", err)
	}
	// Restart the profile store: uncommitted source witnesses must expire, but
	// an already completed binding must not create the profile again.
	adapter.profiles, err = store.NewFileStore(index, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	adapter.defaults = func() (string, string) { t.Fatal("replay resolved a new model"); return "", "" }
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal("lost-response replay did not observe its binding", err)
	}
	if len(adapter.profiles.ListAgents()) != 1 {
		t.Fatal("replay created another profile")
	}
	changed := fill
	changed.Model = "different"
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{changed}); err == nil {
		t.Fatal("old choice digest concealed changed effect material")
	}
	after, _ := workspaces.Get(scope.HomeWorkspaceID)
	if after.Version != home.Version || len(after.GetAgentInstances()) != 1 {
		t.Fatal("replay rewrote the Home")
	}
}

func TestStaffRoleOnWorkspaceReviewedClearRequiresNewPredecessorBoundReview(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Aster", Provider: "openai", Model: "gpt-4o-mini", ChoiceDigest: strings.Repeat("a", 64)}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UnstaffRoleFromWorkspace(t.Context(), scope.HomeWorkspaceID, fill.RoleID); err != nil {
		t.Fatal(err)
	}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err == nil {
		t.Fatal("retry reversed an explicit clear")
	}
	selected := store.ReadStaffingSnapshot(t.Context(), adapter.profiles)
	fresh := RoleFill{RoleID: fill.RoleID, Mode: StaffingModeBind, Name: fill.Name, SourceWitness: selected.Profiles[0].Witness,
		ChoiceDigest: strings.Repeat("b", 64), PreviousChoiceDigest: fill.ChoiceDigest}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fresh}); err != nil {
		t.Fatal("fresh reviewed reuse refused", err)
	}
	home, _ := workspaces.Get(scope.HomeWorkspaceID)
	state := home.GetAssistantProgramState()
	if len(state.StaffingReceipts) != 1 || state.StaffingReceipts[0].ChoiceDigest != fresh.ChoiceDigest || state.StaffingReceipts[0].Action != "assign" {
		t.Fatal("fresh review did not replace the role's bounded receipt")
	}
	if len(adapter.profiles.ListAgents()) != 1 || store.ReadStaffingSnapshot(t.Context(), adapter.profiles).Profiles[0].Witness != selected.Profiles[0].Witness {
		t.Fatal("fresh reuse changed the retained definition")
	}
	if err := adapter.UnstaffRoleFromWorkspace(t.Context(), scope.HomeWorkspaceID, fill.RoleID); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []RoleFill{fill, fresh} {
		if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{stale}); err == nil {
			t.Fatal("old review repeated an effect after clear")
		}
	}
}

func TestStaffRoleOnWorkspaceReviewedOutcomeRejectsPartialOrForeignProof(t *testing.T) {
	cases := map[string]func(*RoleFill){
		"invalid digest":         func(fill *RoleFill) { fill.ChoiceDigest = "invalid" },
		"unknown predecessor":    func(fill *RoleFill) { fill.PreviousChoiceDigest = strings.Repeat("b", 64) },
		"missing digest":         func(fill *RoleFill) { fill.ChoiceDigest = ""; fill.PreviousChoiceDigest = strings.Repeat("b", 64) },
		"project scope":          func(fill *RoleFill) { fill.RoleID = "project_lead" },
		"unreviewed default":     func(fill *RoleFill) { fill.Provider, fill.Model = "", "" },
		"unwitnessed assignment": func(fill *RoleFill) { fill.Mode, fill.Provider, fill.Model = StaffingModeBind, "", "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, workspaces, scope, _ := staffingFixture(t)
			fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Aster", Provider: "openai", Model: "gpt-4o-mini", ChoiceDigest: strings.Repeat("a", 64)}
			mutate(&fill)
			target := scope.HomeWorkspaceID
			if name == "project scope" {
				target = scope.ProjectWorkspaceID
			}
			if err := adapter.StaffRoleOnWorkspace(t.Context(), target, []RoleFill{fill}); err == nil {
				t.Fatal("invalid proof accepted")
			}
			if len(adapter.profiles.ListAgents()) != 0 {
				t.Fatal("refused proof created a profile")
			}
			home, _ := workspaces.Get(scope.HomeWorkspaceID)
			if len(home.GetAssistantProgramState().StaffingReceipts) != 0 {
				t.Fatal("refused proof gained an outcome receipt")
			}
		})
	}
}
