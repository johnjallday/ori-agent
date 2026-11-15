package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects: the Home's shared assistant is created before any
// song exists, with the reservation's marker, and is bound into no workspace.
func TestCreateSharedProjectAgent_StagedAppearance(t *testing.T) {
	adapter, _, _, _ := staffingFixture(t)
	ch := charactercatalog.MustLoad().Working()[0]
	appearance := types.NewAgentAppearance()
	appearance.SetCharacter(string(ch.ID), ch.EntryVersion)
	marker := &agent.AssistantSetupProvenance{ID: "0123456789abcdef0123456789abcdef", RunID: "home"}
	role := workspace.AssistantProgramRoleSpec{ID: "assistant", Role: "orchestrator", SystemPrompt: "Helps"}
	if created, err := adapter.CreateSharedProjectAgent(t.Context(), role, "Assistant", "", "", appearance, marker); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	profile, _ := adapter.profiles.GetAgent("Assistant")
	if profile.Appearance.CharacterCatalogID() != string(ch.ID) || profile.Appearance == appearance {
		t.Fatalf("staged appearance lost or aliased: %+v", profile.Appearance)
	}
	appearance.SetCharacter("unknown-face", 1)
	if created, err := adapter.CreateSharedProjectAgent(t.Context(), role, "Other", "", "", appearance, marker); err == nil || created {
		t.Fatalf("invalid appearance created a profile: %v %v", created, err)
	}
}

func TestCreateSharedProjectAgent(t *testing.T) {
	adapter, workspaces, _, grants := staffingFixture(t)
	role := workspace.AssistantProgramRoleSpec{ID: "project_lead", Label: "Project Lead", Scope: workspace.AssistantRoleScopeProject,
		Required: true, Role: "orchestrator", SystemPrompt: "project-lead-only prompt", Description: "Leads the project.",
		Skills: []string{"project-skill"}}
	marker := &agent.AssistantSetupProvenance{ID: "0123456789abcdef0123456789abcdef", RunID: "home-1",
		OperationID: "shared-project-role:project_lead", ReviewDigest: "d"}
	grants.personal = map[string]bool{"project-skill": true}

	created, err := adapter.CreateSharedProjectAgent(context.Background(), role, "Project Lead", "", "", nil, marker)
	if err != nil || !created {
		t.Fatalf("create = %v %v", created, err)
	}
	profile, found := adapter.profiles.GetAgent("Project Lead")
	if !found || profile.AssistantSetup == nil || *profile.AssistantSetup != *marker {
		t.Fatalf("profile marker = %+v", profile)
	}
	if profile.Settings.SystemPrompt != "project-lead-only prompt" || profile.Settings.Provider != "openai" ||
		profile.Settings.Model != "gpt-4o-mini" {
		t.Fatalf("profile settings = %+v", profile.Settings)
	}
	if !grants.granted["Project Lead"]["project-skill"] || len(grants.personalGrantCalls) != 1 || len(grants.grantCalls) != 0 {
		t.Fatalf("skills: granted %v personal %q plain %q", grants.granted, grants.personalGrantCalls, grants.grantCalls)
	}
	// Bound into no workspace.
	if used, err := workspace.HasWorkspaceAgentReference(context.Background(), workspaces, "Project Lead", ""); err != nil || used {
		t.Fatalf("the shared agent was bound into a workspace: %v %v", used, err)
	}

	// The name taken: nothing is written.
	if created, err := adapter.CreateSharedProjectAgent(context.Background(), role, "Project Lead", "", "", nil, marker); created || !errors.Is(err, ErrConflict) {
		t.Fatalf("second create = %v %v", created, err)
	}
	// No marker, or a model that cannot run: refused before anything is written.
	if created, err := adapter.CreateSharedProjectAgent(context.Background(), role, "Other", "", "", nil, nil); created || err == nil {
		t.Fatalf("no marker = %v %v", created, err)
	}
	if created, err := adapter.CreateSharedProjectAgent(context.Background(), role, "Other", "claude", "nope", nil, marker); created || err == nil {
		t.Fatalf("unrunnable model = %v %v", created, err)
	}
	if _, found := adapter.profiles.GetAgent("Other"); found {
		t.Fatal("a refused create wrote a profile")
	}
}
