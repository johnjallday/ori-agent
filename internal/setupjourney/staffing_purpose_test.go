package setupjourney

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestStaffRoleOnWorkspaceNewProfileKeepsPublicPurposeAndReuseDoesNotRewriteIt(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	purpose := "Coordinates album schedules and maintains the music project catalog."
	setDescription := func(description string) {
		t.Helper()
		if err := workspaces.Update(scope.HomeWorkspaceID, func(home *workspace.Workspace) error {
			state := home.GetAssistantProgramState()
			state.Declaration.Roles[0].Description = description
			home.SetAssistantProgramState(state)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setDescription(purpose)
	fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeCreate, Name: "Aster"}
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal(err)
	}
	profile, ok := adapter.profiles.GetAgent("Aster")
	if !ok || profile.Metadata == nil || profile.Metadata.Description != purpose || profile.Settings.SystemPrompt != "home-only prompt" {
		t.Fatal("public purpose was lost or confused with private instructions")
	}
	before := store.ReadStaffingSnapshot(t.Context(), adapter.profiles)
	if len(before.Profiles) != 1 || before.Profiles[0].Purpose != purpose {
		t.Fatal("new profile's public purpose is not available to read-only staffing")
	}
	if err := adapter.UnstaffRoleFromWorkspace(t.Context(), scope.HomeWorkspaceID, fill.RoleID); err != nil {
		t.Fatal(err)
	}
	setDescription("A different role description must not rewrite an existing saved profile.")
	fill.Mode, fill.SourceWitness = StaffingModeBind, before.Profiles[0].Witness
	if err := adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill}); err != nil {
		t.Fatal(err)
	}
	after := store.ReadStaffingSnapshot(t.Context(), adapter.profiles)
	if len(after.Profiles) != 1 || after.Profiles[0].Witness != before.Profiles[0].Witness || after.Profiles[0].Purpose != purpose {
		t.Fatal("reuse rewrote the saved definition")
	}
}
