package sessionhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// autoFillEnv is a handler that can create the assistant-team blueprint, with
// the staffing seams a real server wires and a switch for "is a model set up".
func autoFillEnv(t *testing.T, modelReady *bool) (*Handler, projecttemplates.Template, func()) {
	t.Helper()
	handler, _, _, cleanup := templateTestEnv(t)
	tpl := strictAssistantReadinessTemplate()
	installed := plugin.InstalledPlugin{
		Name: "readiness", Version: "1.0.0", Enabled: true,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: "readiness", Version: "1.0.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: "assistant-team", QualifiedID: tpl.ID, Version: 1, Template: tpl,
		}},
	}
	handler.SetInstalledPluginLister(readinessPluginLister{installed: []plugin.InstalledPlugin{installed}})
	handler.projectTemplateResolver = func(templateID, templatePath string) (projecttemplates.Template, error) {
		if templateID == tpl.ID && strings.TrimSpace(templatePath) == "" {
			return tpl, nil
		}
		return projecttemplates.Template{}, fmt.Errorf("template not found")
	}
	setTestAssistantRoleStaffer(handler, -1, nil)
	handler.SetSharedProjectStaffing(
		projectstaffing.New(handler.workspaceTaskStore, handler.agentStore),
		func() bool { return *modelReady },
	)
	return handler, tpl, cleanup
}

func strictCreateBody(tpl projecttemplates.Template, plan templateAgentPlan, name, staffing string) string {
	return fmt.Sprintf(`{"name":%q,"template_id":%q,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":%s}`,
		name, tpl.ID, plan.Revision, staffing)
}

func TestReviewedCreateFillsTheRequiredRoleTheUserLeftEmpty(t *testing.T) {
	ready := true
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()
	plan := handler.buildTemplateAgentPlan(tpl)

	w, response := postCreateWorkspace(t, handler, strictCreateBody(tpl, plan, "Night Drive", "[]"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	completion, _ := response["team_completion"].(map[string]any)
	if completion["state"] != "complete" || completion["team_staffed"] != true {
		t.Fatalf("team completion = %#v", response["team_completion"])
	}
	if warning, present := response["project_warning"]; present {
		t.Fatalf("a filled team still warned: %v", warning)
	}
	folder, _ := response["folder"].(map[string]any)
	workspaceID, _ := folder["id"].(string)
	project, err := handler.workspaceTaskStore.Get(workspaceID)
	if err != nil || project == nil {
		t.Fatalf("read project: %v", err)
	}
	// The required primary role got a new agent named after the role, and it is
	// the workspace's entry agent. The optional role stays the user's choice.
	instances := project.GetAgentInstances()
	if len(instances) != 1 || instances[0].RoleID != "lead" || instances[0].Name != "Lead" ||
		instances[0].RoleSource != agentworkspace.RoleSourceCreated {
		t.Fatalf("project agents = %#v", instances)
	}
	if entry := project.EntryAgentName(); entry != "Lead" {
		t.Fatalf("entry agent = %q, want Lead", entry)
	}
	if _, found := handler.agentStore.GetAgent("Lead"); !found {
		t.Fatal("the role's agent was not created")
	}
}

func TestReviewedCreateKeepsTheUsersOwnFillAndAddsNothingElse(t *testing.T) {
	ready := true
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()
	plan := handler.buildTemplateAgentPlan(tpl)

	w, response := postCreateWorkspace(t, handler, strictCreateBody(tpl, plan, "Own Choice",
		`[{"role_id":"lead","mode":"create","name":"My Producer"}]`))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	folder, _ := response["folder"].(map[string]any)
	project, err := handler.workspaceTaskStore.Get(folder["id"].(string))
	if err != nil || project == nil {
		t.Fatalf("read project: %v", err)
	}
	instances := project.GetAgentInstances()
	if len(instances) != 1 || instances[0].Name != "My Producer" {
		t.Fatalf("project agents = %#v", instances)
	}
	if _, found := handler.agentStore.GetAgent("Lead"); found {
		t.Fatal("a role the user filled was also filled by the host")
	}
}

func TestReviewedCreateWithoutAModelNamesTheRoleItCannotFill(t *testing.T) {
	ready := false
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()
	plan := handler.buildTemplateAgentPlan(tpl)

	w, response := postCreateWorkspace(t, handler, strictCreateBody(tpl, plan, "No Model", "[]"))
	if w.Code != http.StatusConflict || response["code"] != "team_readiness" {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	message, _ := response["error"].(string)
	if !strings.HasPrefix(message, "Lead: no model is set up for new agents") {
		t.Fatalf("blocker = %q", message)
	}
	assertNoReadinessSideEffects(t, handler, "Lead")

	// The user's own choice of a saved agent needs no model and is not blocked.
	if err := handler.agentStore.CreateAgent("Saved Lead", nil); err != nil {
		t.Fatal(err)
	}
	w, _ = postCreateWorkspace(t, handler, strictCreateBody(tpl, plan, "Assigned",
		`[{"role_id":"lead","mode":"assign","name":"Saved Lead"}]`))
	if w.Code != http.StatusCreated {
		t.Fatalf("assigning a saved agent: status = %d: %s", w.Code, w.Body.String())
	}
}

func TestACreateWithoutTheReviewedWizardFillsNothingByItself(t *testing.T) {
	ready := true
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()

	w, response := postCreateWorkspace(t, handler, fmt.Sprintf(`{"name":"Legacy Caller","template_id":%q,"role_staffing":[]}`, tpl.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	folder, _ := response["folder"].(map[string]any)
	project, err := handler.workspaceTaskStore.Get(folder["id"].(string))
	if err != nil || project == nil {
		t.Fatalf("read project: %v", err)
	}
	if got := len(project.GetAgentInstances()); got != 0 {
		t.Fatalf("an API caller's empty roles were filled: %d agents", got)
	}
}

func TestTemplateAgentPlanListsHowRequiredRolesAreFilled(t *testing.T) {
	ready := true
	handler, tpl, cleanup := autoFillEnv(t, &ready)
	defer cleanup()

	read := func() templateAgentPlan {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan",
			strings.NewReader(fmt.Sprintf(`{"template_id":%q}`, tpl.ID)))
		recorder := httptest.NewRecorder()
		handler.handleTemplateAgentPlan(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("plan status = %d: %s", recorder.Code, recorder.Body.String())
		}
		var plan templateAgentPlan
		if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}

	plan := read()
	if len(plan.AutoStaffing) != 1 {
		t.Fatalf("auto staffing = %#v", plan.AutoStaffing)
	}
	row := plan.AutoStaffing[0]
	if row.RoleID != "lead" || row.Label != "Lead" || row.Scope != "project" || row.Outcome != roleAutoFillCreate ||
		row.AgentName != "Lead" || row.Blocked != "" {
		t.Fatalf("row = %#v", row)
	}
	revision := plan.Revision

	// A name taken in between changes the agent's planned name, never the
	// reviewed plan's revision; a missing model blocks the role with its reason.
	if err := handler.agentStore.CreateAgent("Lead", nil); err != nil {
		t.Fatal(err)
	}
	ready = false
	plan = read()
	if plan.Revision != revision {
		t.Fatal("a planned agent name changed the reviewed plan revision")
	}
	if row := plan.AutoStaffing[0]; row.AgentName != "Lead 2" || row.Blocked == "" {
		t.Fatalf("row = %#v", row)
	}
}
