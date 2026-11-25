package sessionhttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspaceroles"
)

func newTemplateAgentTestHandler(t *testing.T) *Handler {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	return handler
}

func TestWorkspaceManagerBaseName(t *testing.T) {
	for input, want := range map[string]string{
		"House":                  "House Manager",
		"  House  ":              "House Manager",
		"House & Garden":         "House Garden Manager",
		"Café Notes":             "Caf Notes Manager",
		"Studio Manager":         "Studio Manager",
		"manager":                "manager",
		"":                       "Manager",
		"!!!":                    "Manager",
		strings.Repeat("a", 200): strings.Repeat("a", maxManagerNameLength-len(managerNameSuffix)) + managerNameSuffix,
	} {
		got := workspaceManagerBaseName(input)
		if got != want {
			t.Errorf("workspaceManagerBaseName(%q) = %q, want %q", input, got, want)
		}
		if err := validateTemplateAgentOverrideName(got); err != nil {
			t.Errorf("proposed name %q would be refused by review: %v", got, err)
		}
	}
}

func TestProposeWorkspaceManagerName_NeverReusesAnExistingAgent(t *testing.T) {
	handler := newTemplateAgentTestHandler(t)
	if got := handler.proposeWorkspaceManagerName("House"); got != "House Manager" {
		t.Fatalf("fresh proposal = %q", got)
	}
	for _, taken := range []string{"House Manager", "house manager 2"} {
		if err := handler.agentStore.CreateAgent(taken, nil); err != nil {
			t.Fatalf("CreateAgent(%q): %v", taken, err)
		}
	}
	if got := handler.proposeWorkspaceManagerName("House"); got != "House Manager 3" {
		t.Fatalf("proposal with House Manager and house manager 2 taken = %q, want House Manager 3", got)
	}
}

func TestProposeWorkspaceManagerName_SkipsRetiredAssistantNames(t *testing.T) {
	handler := newTemplateAgentTestHandler(t)
	// "Workspace" would propose "Workspace Manager", a name the strict
	// staffing create refuses while the former assistant's names are reserved.
	if got := handler.proposeWorkspaceManagerName("Workspace"); got != "Workspace Manager 2" {
		t.Fatalf("proposal = %q", got)
	}
}

func TestHostRoleIdentityDoesNotFollowTheAgentName(t *testing.T) {
	specs := []projecttemplates.AgentSpec{{Name: "House Manager", HostRoleID: blankManagerRoleID, HostRoleLabel: blankManagerRoleLabel}}
	renamed := []projecttemplates.AgentSpec{{Name: "Home Manager", HostRoleID: blankManagerRoleID, HostRoleLabel: blankManagerRoleLabel}}

	for _, roster := range [][]projecttemplates.AgentSpec{specs, renamed} {
		if ids := projecttemplates.AgentRoleIDs(roster); len(ids) != 1 || ids[0] != blankManagerRoleID {
			t.Fatalf("role ids for %q = %v, want [%s]", roster[0].Name, ids, blankManagerRoleID)
		}
		roles := workspaceroles.FromTemplateAgents(roster)
		if len(roles) != 1 || roles[0].ID != blankManagerRoleID || roles[0].Label != blankManagerRoleLabel || !roles[0].Primary {
			t.Fatalf("declared role for %q = %#v", roster[0].Name, roles)
		}
	}

	plan := newTemplateAgentTestHandler(t).buildTemplateAgentPlan(projecttemplates.Template{ID: "blank", Agents: specs})
	if len(plan.Agents) != 1 || plan.Agents[0].RoleID != blankManagerRoleID || plan.Agents[0].RoleLabel != blankManagerRoleLabel {
		t.Fatalf("plan does not carry the host role identity: %#v", plan.Agents)
	}
	ordinary := newTemplateAgentTestHandler(t).buildTemplateAgentPlan(projecttemplates.Template{ID: "x", Agents: []projecttemplates.AgentSpec{{Name: "Scout"}}})
	if ordinary.Agents[0].RoleID != "" || ordinary.Agents[0].RoleLabel != "" {
		t.Fatalf("an authorable blueprint must not gain host role fields: %#v", ordinary.Agents[0])
	}
}

func TestLegacyBlankRoleIDs(t *testing.T) {
	for _, id := range []string{"ask-ori", "Workspace-Manager"} {
		if !isLegacyBlankRoleID(id) {
			t.Errorf("%q should map onto the manager slot", id)
		}
	}
	for _, id := range []string{"manager", "house-manager", "ori", ""} {
		if isLegacyBlankRoleID(id) {
			t.Errorf("%q must not map onto the manager slot", id)
		}
	}
}
