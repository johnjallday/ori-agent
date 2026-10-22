package sessionhttp

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspaceroles"
)

// A Researcher or Writer hired from the Agents page gets its role's starter
// blueprint, with the hired agent — already created — in the primary role and
// every other declared role open (agents-hire-with-a-brief FR8b, FR11).
func TestCreateHireWorkspace_PutsTheHiredAgentInThePrimaryRoleAndLeavesTheRestOpen(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		template, agent string
		role            types.AgentRole
		primary         string
		open            []string
	}{
		{"research-project", "Rosa", types.RoleResearcher, "research-lead", []string{"source-scout", "synthesis-writer"}},
		{"writing-project", "Wren", types.RoleSynthesizer, "writing-coach", []string{"chapter-drafter", "line-editor"}},
	} {
		t.Run(tc.template, func(t *testing.T) {
			if err := handler.agentStore.CreateAgent(tc.agent, &store.CreateAgentConfig{Role: tc.role}); err != nil {
				t.Fatal(err)
			}
			agentsBefore := len(handler.agentStore.ListAgents())
			request := HireWorkspaceRequest{
				Name: "Hire " + tc.agent, TemplateID: tc.template, AgentName: tc.agent,
				Mission: "Compare the three note-taking apps I'm considering and recommend one",
			}
			id, err := handler.CreateHireWorkspace(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			ws, err := handler.workspaceStore.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if ws.EntryAgentName() != tc.agent || ws.Mission != request.Mission || !ws.GoalFirstRunPending {
				t.Fatalf("entry %q goal %q armed %v", ws.EntryAgentName(), ws.Mission, ws.GoalFirstRunPending)
			}
			if got := len(handler.agentStore.ListAgents()); got != agentsBefore {
				t.Fatalf("the blueprint created agents: %d → %d", agentsBefore, got)
			}
			if provenance := handler.workspaceTemplateProvenance(ws); provenance == nil || provenance.TemplateID != tc.template {
				t.Fatalf("provenance %+v", provenance)
			}
			roster := handler.buildWorkspaceRoster(ws)
			states := map[string]workspaceroles.RoleProjection{}
			for _, role := range roster.Roles {
				states[role.RoleID] = role
			}
			primary := states[tc.primary]
			if !primary.Primary || primary.State != workspaceroles.StateFilled || primary.Agent == nil || primary.Agent.Name != tc.agent {
				t.Fatalf("primary role %+v", primary)
			}
			for _, roleID := range tc.open {
				if states[roleID].State != workspaceroles.StateEmpty {
					t.Fatalf("role %s is %q, want open", roleID, states[roleID].State)
				}
			}

			// The same name again is a taken name; the hire picks the next.
			if _, err := handler.CreateHireWorkspace(t.Context(), request); !errors.Is(err, ErrHireWorkspaceNameTaken) {
				t.Fatalf("a taken name: %v", err)
			}
			// A failed hire removes the workspace through the UI's own delete.
			if err := handler.DeleteHireWorkspace(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if _, err := handler.workspaceStore.Get(id); err == nil {
				t.Fatal("the hire's workspace outlived its removal")
			}
		})
	}
}

func TestCreateHireWorkspace_RefusesAnIncompleteRequestOrUnknownBlueprint(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if _, err := handler.CreateHireWorkspace(t.Context(), HireWorkspaceRequest{Name: "X", TemplateID: "research-project"}); err == nil {
		t.Fatal("a request with no agent was accepted")
	}
	if _, err := handler.CreateHireWorkspace(t.Context(), HireWorkspaceRequest{Name: "X", TemplateID: "no-such-blueprint", AgentName: "Rosa"}); err == nil {
		t.Fatal("an unknown blueprint was accepted")
	}
}
