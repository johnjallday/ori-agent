package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderAgentHireEdits_CreateHomeProfileWithExactSettingsOnly(t *testing.T) {
	b, homeID, face := folderAgentFacesFixture(t)
	edit := folderagent.HireEdit{Name: "Aster", Provider: "chosen-provider", Model: "chosen-model"}
	staffing := portfolioHomeStaffing{builder: b, edits: map[string]folderagent.HireEdit{"manager": edit}, faces: map[string]*types.AgentAppearance{edit.Name: face}}
	roles, err := staffing.Missing(t.Context(), homeID)
	if err != nil || len(roles) != 1 || roles[0].RoleID != "manager" || roles[0].Name != edit.Name {
		t.Fatalf("pending role: %+v %v", roles, err)
	}
	if len(b.st.ListAgents()) != 0 {
		t.Fatal("read-only projection created an agent")
	}
	if err := staffing.Staff(t.Context(), homeID, roles[0]); err != nil {
		t.Fatal(err)
	}
	profile, ok := b.st.GetAgent(edit.Name)
	if !ok || profile.Settings.Model != edit.Model || profile.Settings.Provider != edit.Provider || profile.Settings.SystemPrompt != "Coordinates work" || profile.Role != "orchestrator" || profile.Appearance.CharacterCatalogID() != face.CharacterCatalogID() {
		t.Fatalf("saved profile changed role or lost draft settings: %+v", profile)
	}
	// Already-filled roles are not returned or edited by subsequent setups.
	staffing.edits["manager"] = folderagent.HireEdit{Name: "Another", Provider: "other", Model: "other"}
	roles, err = staffing.Missing(t.Context(), homeID)
	if err != nil || len(roles) != 0 || len(b.st.ListAgents()) != 1 {
		t.Fatalf("adopted/edited an existing role: %+v %v", roles, err)
	}
	profile, _ = b.st.GetAgent(edit.Name)
	if profile.Settings.Model != edit.Model || profile.Settings.SystemPrompt != "Coordinates work" {
		t.Fatal("existing profile overwritten")
	}
}

func TestFolderAgentHireEdits_SharedProfileKeepsRoleAndReservationGuards(t *testing.T) {
	b, homeID, face := folderAgentFacesFixture(t)
	b.projectStaffing = projectstaffing.New(b.workspaceStore, b.st)
	team := projectstaffing.Team{PluginID: "fixture", BlueprintID: "song", Digest: strings.Repeat("c", 64), Roles: []workspace.AssistantProgramRoleSpec{{ID: "assistant", Label: "App Assistant", Required: true, Scope: workspace.AssistantRoleScopeProject, Role: "orchestrator", SystemPrompt: "Fixed project instructions"}}}
	plan, err := b.projectStaffing.PlanShared(homeID, team)
	if err != nil {
		t.Fatal(err)
	}
	edit := folderagent.HireEdit{Name: "Muse", Provider: "chosen-provider", Model: "chosen-model"}
	run := folderAgentAssistantRun{line: "Hires Muse", homeID: homeID, team: team, teamOK: true, plan: plan, edit: &folderAgentAssistantEdit{roleID: "assistant", value: edit}}
	rec := folderagent.Record{WorkspaceID: "folder", HireFaces: map[string]*types.AgentAppearance{edit.Name: face}}
	name, stopped := b.hireFolderAgentAssistant(t.Context(), rec, run, true, func(_, _, _ string) {})
	if stopped != nil || name != edit.Name {
		t.Fatalf("shared hire: %q %+v", name, stopped)
	}
	profile, _ := b.st.GetAgent(edit.Name)
	if profile.Settings.Model != edit.Model || profile.Settings.Provider != edit.Provider || profile.Role != "orchestrator" || profile.Settings.SystemPrompt != "Fixed project instructions" || profile.Appearance.CharacterCatalogID() != face.CharacterCatalogID() {
		t.Fatalf("shared settings: %+v", profile)
	}
	consent, err := b.projectStaffing.Consents().Read(homeID)
	if err != nil || consent.TeamDigest != team.Digest || len(consent.Roles) != 1 || consent.Roles[0].RoleID != "assistant" || consent.Roles[0].AgentName != edit.Name || consent.Roles[0].PendingName != "" {
		t.Fatalf("changed role authority: %+v %v", consent, err)
	}
	run.edit.value.Model = "must-not-overwrite"
	if name, stopped := b.hireFolderAgentAssistant(t.Context(), rec, run, true, func(_, _, _ string) {}); stopped != nil || name != "" {
		t.Fatalf("retry: %q %+v", name, stopped)
	}
	profile, _ = b.st.GetAgent(edit.Name)
	if profile.Settings.Model != edit.Model || len(b.st.ListAgents()) != 1 {
		t.Fatal("existing shared profile overwritten")
	}
}
