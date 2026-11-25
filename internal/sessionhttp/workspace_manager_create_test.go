package sessionhttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Group 2 contract: a new Blank workspace gets a manager of its own. These
// tests drive the real create handler and check what is persisted.

func createStaffedBlank(t *testing.T, h *Handler, workspaceName string) (*httptestResult, string) {
	t.Helper()
	plan := h.buildTemplateAgentPlan(h.blankWorkspaceTemplate(workspaceName))
	body := fmt.Sprintf(`{"name":%q,"blank":true,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"manager","mode":"create","name":%q}]}`,
		workspaceName, plan.Revision, plan.EntryAgentName)
	w, response := postCreateWorkspace(t, h, body)
	result := &httptestResult{code: w.Code, body: w.Body.String(), response: response, plan: plan}
	if w.Code != http.StatusCreated {
		return result, ""
	}
	folder, _ := response["folder"].(map[string]any)
	id, _ := folder["id"].(string)
	return result, id
}

type httptestResult struct {
	code     int
	body     string
	response map[string]any
	plan     templateAgentPlan
}

func TestBlankCreate_EachWorkspaceGetsItsOwnBoundManager(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	houseResult, houseID := createStaffedBlank(t, h, "House")
	if houseID == "" {
		t.Fatalf("House create = %d: %s", houseResult.code, houseResult.body)
	}
	garageResult, garageID := createStaffedBlank(t, h, "Garage")
	if garageID == "" {
		t.Fatalf("Garage create = %d: %s", garageResult.code, garageResult.body)
	}

	for id, name := range map[string]string{houseID: "House Manager", garageID: "Garage Manager"} {
		ws, err := h.store.GetWorkspace(context.Background(), id)
		if err != nil || ws == nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got := ws.SharedData[workspaceEntryAgentNameKey]; got != name {
			t.Fatalf("workspace %s entry = %v, want %q", id, got, name)
		}
		if len(ws.AgentInstances) != 1 {
			t.Fatalf("workspace %s attached %d agents, want only its manager", id, len(ws.AgentInstances))
		}
		profile, ok := h.agentStore.GetAgent(name)
		if !ok || !profile.WorkspaceManager.ManagesWorkspace(id) {
			t.Fatalf("%s provenance = %#v, want workspace %s", name, profile, id)
		}
	}
}

func TestBlankCreate_DuplicateSubmitNeverReusesTheFirstManager(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	first, firstID := createStaffedBlank(t, h, "House")
	if firstID == "" {
		t.Fatalf("first create = %d: %s", first.code, first.body)
	}
	// A second tab submits the review it loaded before the first create.
	body := fmt.Sprintf(`{"name":"House","blank":true,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"manager","mode":"create","name":"House Manager"}]}`, first.plan.Revision)
	w, response := postCreateWorkspace(t, h, body)
	if w.Code == http.StatusCreated {
		t.Fatalf("a stale duplicate submit created a second workspace: %s", w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate submit status = %d (%v), want a conflict", w.Code, response["code"])
	}
	workspaces, err := h.store.ListWorkspaces(context.Background())
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("workspaces after duplicate submit = %d, err=%v", len(workspaces), err)
	}
}

func TestBlankCreate_ReloadedPlanProposesAFreshManager(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := h.agentStore.CreateAgent("House Manager", nil); err != nil {
		t.Fatal(err)
	}

	result, id := createStaffedBlank(t, h, "House")
	if id == "" {
		t.Fatalf("create = %d: %s", result.code, result.body)
	}
	if result.plan.EntryAgentName != "House Manager 2" {
		t.Fatalf("proposal beside an existing House Manager = %q", result.plan.EntryAgentName)
	}
	existing, _ := h.agentStore.GetAgent("House Manager")
	if existing.WorkspaceManager != nil {
		t.Fatal("the pre-existing similar-label agent was bound to the new workspace")
	}
}

func TestBlankCreate_OpeningAnExistingRosterChangesNothing(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	plan := h.buildTemplateAgentPlan(h.blankWorkspaceTemplate("Quiet"))
	body := fmt.Sprintf(`{"name":"Quiet","blank":true,"create_template_agents":false,"team_intent":{"version":1,"mode":"agentless","plan_revision":%q},"role_staffing":[]}`, plan.Revision)
	w, response := postCreateWorkspace(t, h, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	folder, _ := response["folder"].(map[string]any)
	id, _ := folder["id"].(string)
	before := h.agentStore.ListAgents()

	current, err := h.workspaceTaskStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		roster := h.buildWorkspaceRoster(current)
		if roster.FilledCount != 0 || roster.Roles[0].State != "empty" {
			t.Fatalf("reading an agentless roster filled it: %#v", roster)
		}
	}
	after, err := h.workspaceTaskStore.Get(id)
	if err != nil || len(after.GetAgentInstances()) != 0 || after.EntryAgentName() != "" {
		t.Fatalf("reading the roster mutated the workspace: %#v", after)
	}
	if got := h.agentStore.ListAgents(); len(got) != len(before) {
		t.Fatalf("reading the roster created agents: before=%v after=%v", before, got)
	}
}

func TestBlankRoster_LegacyLeadStaysInTheManagerSlot(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := h.agentStore.CreateAgent("Fresh Lead", nil); err != nil {
		t.Fatal(err)
	}
	current := &agentworkspace.Workspace{ID: "ws-legacy", Name: "Old Blank"}
	current.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: blankWorkspaceTemplateID})
	current.AgentInstances = []agentworkspace.AgentInstance{{Name: "Fresh Lead", EntryPoint: true, RoleID: "ask-ori", RoleSource: agentworkspace.RoleSourceCreated}}

	roster := h.buildWorkspaceRoster(current)
	if len(roster.Roles) != 1 || roster.Roles[0].State != "filled" || roster.Roles[0].Agent == nil || roster.Roles[0].Agent.Name != "Fresh Lead" {
		t.Fatalf("legacy lead roster = %#v", roster)
	}
	if len(roster.Unassigned) != 0 {
		t.Fatalf("legacy lead was listed as unassigned: %#v", roster.Unassigned)
	}
	if blankLeadVacant(current) {
		t.Fatal("a legacy lead must keep the manager slot occupied")
	}
	if current.AgentInstances[0].RoleID != "ask-ori" {
		t.Fatal("reading the roster rewrote the stored role id")
	}
}

func stampManagerEntry(t *testing.T, h *Handler, id, name, managerOf string, source string) {
	t.Helper()
	if err := h.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{WorkspaceManager: &agent.WorkspaceManagerProvenance{WorkspaceID: managerOf}}); err != nil {
		t.Fatal(err)
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	ws.AgentInstances = []agentworkspace.AgentInstance{{ID: "entry-" + id, Name: name, EntryPoint: true, RoleID: blankManagerRoleID, RoleSource: source}}
	if err := ws.SetEntryAgentName(name); err != nil {
		t.Fatal(err)
	}
	if err := h.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteWorkspace_RemovesItsOwnManager(t *testing.T) {
	h, _ := createTestHandler(t)
	newTestFileStore(t, h)
	id := createTestWorkspace(t, h, "House")
	stampManagerEntry(t, h, id, "House Manager", id, agentworkspace.RoleSourceCreated)
	if err := h.store.CreateSession(context.Background(), &session.Session{ID: "house-chat", Title: "Quotes", AgentName: "House Manager"}); err != nil {
		t.Fatal(err)
	}

	deleteWorkspacePermanently(t, h, id, "")
	if _, exists := h.agentStore.GetAgent("House Manager"); exists {
		t.Fatal("the deleted workspace's own manager was retained")
	}
}

func TestDeleteWorkspace_RetainsManagersItDoesNotOwn(t *testing.T) {
	for _, scenario := range []string{"another_workspaces_manager", "assigned", "still_referenced"} {
		t.Run(scenario, func(t *testing.T) {
			h, _ := createTestHandler(t)
			newTestFileStore(t, h)
			id := createTestWorkspace(t, h, "House")
			managerOf, source := id, agentworkspace.RoleSourceCreated
			switch scenario {
			case "another_workspaces_manager":
				managerOf = "ws-somewhere-else"
			case "assigned":
				source = agentworkspace.RoleSourceAssigned
			}
			stampManagerEntry(t, h, id, "House Manager", managerOf, source)
			if scenario == "still_referenced" {
				otherID := createTestWorkspace(t, h, "Shed")
				other, err := h.workspaceStore.Get(otherID)
				if err != nil {
					t.Fatal(err)
				}
				other.AgentInstances = []agentworkspace.AgentInstance{{ID: "shared", Name: "House Manager"}}
				if err := h.workspaceStore.Save(other); err != nil {
					t.Fatal(err)
				}
			}

			deleteWorkspacePermanently(t, h, id, "")
			if _, exists := h.agentStore.GetAgent("House Manager"); !exists {
				t.Fatalf("%s: deleting the workspace removed a manager it did not solely own", scenario)
			}
		})
	}
}
