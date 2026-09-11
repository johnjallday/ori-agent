package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func strictReadinessTemplate() projecttemplates.Template {
	return projecttemplates.Template{
		ID: "strict-team", Name: "Strict Team",
		Agents: []projecttemplates.AgentSpec{
			{Name: "Lead", SystemPrompt: "Lead the work."},
			{Name: "Optional Scout", SystemPrompt: "Research when requested."},
		},
	}
}

type readinessPluginLister struct {
	installed []plugin.InstalledPlugin
}

func (l readinessPluginLister) List() ([]plugin.InstalledPlugin, error) {
	return l.installed, nil
}

func strictAssistantReadinessTemplate() projecttemplates.Template {
	return projecttemplates.Template{
		ID:   "plugin:readiness:assistant-team",
		Name: "Assistant Team",
		PluginOwner: &agentworkspace.PluginTemplateOwner{
			PluginID: "readiness", BlueprintID: "assistant-team", BlueprintVersion: 1,
		},
		AssistantProgram: &agentworkspace.AssistantProgramDeclaration{
			SchemaVersion: agentworkspace.AssistantProgramSchemaVersion,
			ID:            "readiness-team",
			StationName:   "Readiness Team Home",
			Roles: []agentworkspace.AssistantProgramRoleSpec{
				{ID: "lead", Label: "Lead", Scope: agentworkspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Lead."},
				{ID: "reviewer", Label: "Reviewer", Scope: agentworkspace.AssistantRoleScopeProject, SystemPrompt: "Review."},
			},
			Stages: []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		},
	}
}

// setTestAssistantRoleStaffer installs a narrow test double for the compiled
// setup-journey callback. It writes the same durable project attachments and
// scoped bindings the observer reads, then can fail after a bounded prefix to
// exercise the non-transactional recovery contract.
func setTestAssistantRoleStaffer(handler *Handler, applyCount int, finalErr error) {
	handler.SetAssistantRoleStaffer(func(_ context.Context, workspaceID string, fills []RoleStaffingFill) error {
		if handler.workspaceTaskStore == nil {
			return fmt.Errorf("workspace task store unavailable")
		}
		current, err := handler.workspaceTaskStore.Get(workspaceID)
		if err != nil || current == nil {
			return fmt.Errorf("read project: %w", err)
		}
		link := current.GetAssistantProjectLink()
		if link == nil {
			return fmt.Errorf("assistant project link unavailable")
		}
		station, err := handler.workspaceTaskStore.Get(link.StationWorkspaceID)
		if err != nil || station == nil || station.GetAssistantProgramState() == nil {
			return fmt.Errorf("read assistant station: %w", err)
		}
		declaration := station.GetAssistantProgramState().Declaration
		roles := make(map[string]agentworkspace.AssistantProgramRoleSpec, len(declaration.Roles))
		for _, role := range declaration.Roles {
			roles[role.ID] = role
		}
		limit := applyCount
		if limit < 0 || limit > len(fills) {
			limit = len(fills)
		}
		applied := append([]RoleStaffingFill(nil), fills[:limit]...)
		for _, fill := range applied {
			if fill.Mode == roleStaffingModeCreate {
				if err := handler.agentStore.CreateAgent(fill.Name, nil); err != nil {
					return err
				}
			}
		}
		if err := handler.workspaceTaskStore.Update(workspaceID, func(project *agentworkspace.Workspace) error {
			for _, fill := range applied {
				role := roles[fill.RoleID]
				source := agentworkspace.RoleSourceAssigned
				if fill.Mode == roleStaffingModeCreate {
					source = agentworkspace.RoleSourceCreated
				}
				project.AgentInstances = append(project.AgentInstances, agentworkspace.AgentInstance{
					ID: fill.RoleID + "-instance", Name: fill.Name, RoleID: fill.RoleID,
					RoleSource: source, EntryPoint: role.Primary,
				})
				if role.Primary {
					if err := project.SetEntryAgentName(fill.Name); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		bindings := make([]agentworkspace.AssistantRoleBinding, 0, len(applied))
		for _, fill := range applied {
			bindings = append(bindings, agentworkspace.AssistantRoleBinding{
				RoleID: fill.RoleID, AgentInstanceID: fill.RoleID + "-instance", AgentName: fill.Name,
			})
		}
		if err := agentworkspace.NewAssistantProgramStore(handler.workspaceTaskStore).SetProjectRoleBindings(
			workspaceID, link.ProjectBindings.StateRevision, bindings,
		); err != nil {
			return err
		}
		return finalErr
	})
}

func strictReadinessHandler(t *testing.T) (*Handler, templateAgentPlan, func()) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	tpl := strictReadinessTemplate()
	handler.projectTemplateResolver = func(templateID, templatePath string) (projecttemplates.Template, error) {
		if strings.TrimSpace(templateID) == tpl.ID && strings.TrimSpace(templatePath) == "" {
			return tpl, nil
		}
		return projecttemplates.Template{}, fmt.Errorf("template not found")
	}
	return handler, handler.buildTemplateAgentPlan(tpl), cleanup
}

func assertNoReadinessSideEffects(t *testing.T, handler *Handler, names ...string) {
	t.Helper()
	workspaces, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("strict validation created %d workspaces", len(workspaces))
	}
	for _, name := range names {
		if _, found := handler.agentStore.GetAgent(name); found {
			t.Fatalf("strict validation created agent %q", name)
		}
	}
}

func TestWorkspaceTeamIntentAbsencePreservesLegacyBranches(t *testing.T) {
	t.Run("staffing absent keeps template auto-seeding", func(t *testing.T) {
		handler, _, cleanup := strictReadinessHandler(t)
		defer cleanup()
		w, _ := postCreateWorkspace(t, handler, `{"name":"Legacy Seeded","template_id":"strict-team"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		for _, name := range []string{"Lead", "Optional Scout"} {
			if _, found := handler.agentStore.GetAgent(name); !found {
				t.Fatalf("legacy create did not seed %q", name)
			}
		}
	})

	t.Run("explicit empty legacy staffing keeps vacancy behavior", func(t *testing.T) {
		handler, _, cleanup := strictReadinessHandler(t)
		defer cleanup()
		w, _ := postCreateWorkspace(t, handler, `{"name":"Legacy Vacant","template_id":"strict-team","role_staffing":[]}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		for _, name := range []string{"Lead", "Optional Scout"} {
			if _, found := handler.agentStore.GetAgent(name); found {
				t.Fatalf("legacy empty staffing unexpectedly seeded %q", name)
			}
		}
	})
}

func TestWorkspaceTeamIntentMalformedFailsClosedBeforeCreation(t *testing.T) {
	tests := []struct {
		name     string
		intent   string
		staffing string
		status   int
		code     string
	}{
		{name: "null intent", intent: "null", staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "empty object", intent: `{}`, staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "unknown field", intent: `{"version":1,"mode":"staffed","plan_revision":"revision","trusted":true}`, staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "unknown version", intent: `{"version":2,"mode":"staffed","plan_revision":"revision"}`, staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "unknown mode", intent: `{"version":1,"mode":"optional","plan_revision":"revision"}`, staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "blank revision", intent: `{"version":1,"mode":"staffed","plan_revision":""}`, staffing: "[]", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "missing staffing", intent: `{"version":1,"mode":"staffed","plan_revision":"revision"}`, staffing: "", status: http.StatusBadRequest, code: "team_intent_invalid"},
		{name: "null staffing", intent: `{"version":1,"mode":"staffed","plan_revision":"revision"}`, staffing: "null", status: http.StatusBadRequest, code: "team_intent_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, cleanup := strictReadinessHandler(t)
			defer cleanup()
			staffing := ""
			if tt.staffing != "" {
				staffing = `,"role_staffing":` + tt.staffing
			}
			body := fmt.Sprintf(`{"name":"Rejected","template_id":"strict-team","team_intent":%s%s}`, tt.intent, staffing)
			w, response := postCreateWorkspace(t, handler, body)
			if w.Code != tt.status || response["code"] != tt.code {
				t.Fatalf("status/code = %d/%v, want %d/%s: %s", w.Code, response["code"], tt.status, tt.code, w.Body.String())
			}
			assertNoReadinessSideEffects(t, handler)
		})
	}
}

func TestWorkspaceTeamReadinessRejectsStaleMalformedAndInsufficientFills(t *testing.T) {
	tests := []struct {
		name     string
		revision string
		staffing string
		agent    string
	}{
		{name: "stale plan", revision: "stale", staffing: `[{"role_id":"lead","mode":"create","name":"Stale Lead"}]`, agent: "Stale Lead"},
		{name: "missing required role", staffing: `[]`},
		{name: "unknown role", staffing: `[{"role_id":"retired","mode":"create","name":"Unknown Holder"}]`, agent: "Unknown Holder"},
		{name: "duplicate role", staffing: `[{"role_id":"lead","mode":"create","name":"First"},{"role_id":"lead","mode":"create","name":"Second"}]`, agent: "First"},
		{name: "duplicate holder", staffing: `[{"role_id":"lead","mode":"create","name":"Same"},{"role_id":"optional-scout","mode":"create","name":"same"}]`, agent: "Same"},
		{name: "missing mode", staffing: `[{"role_id":"lead","name":"No Mode"}]`, agent: "No Mode"},
		{name: "deleted assignment", staffing: `[{"role_id":"lead","mode":"assign","name":"Deleted Agent"}]`},
		{name: "saved create name collision", staffing: `[{"role_id":"lead","mode":"create","name":"saved"}]`, agent: "saved"},
		{name: "assign setup mutation", staffing: `[{"role_id":"lead","mode":"assign","name":"Saved","model":"other"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, plan, cleanup := strictReadinessHandler(t)
			defer cleanup()
			if tt.name == "assign setup mutation" || tt.name == "saved create name collision" {
				if err := handler.agentStore.CreateAgent("Saved", nil); err != nil {
					t.Fatal(err)
				}
			}
			revision := tt.revision
			if revision == "" {
				revision = plan.Revision
			}
			body := fmt.Sprintf(`{"name":"Rejected","template_id":"strict-team","team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":%s}`, revision, tt.staffing)
			w, response := postCreateWorkspace(t, handler, body)
			if w.Code != http.StatusConflict || response["code"] != "team_readiness" {
				t.Fatalf("status/code = %d/%v, want 409/team_readiness: %s", w.Code, response["code"], w.Body.String())
			}
			assertNoReadinessSideEffects(t, handler, tt.agent)
		})
	}
}

func TestWorkspaceTeamReadinessKeepsDeclaredPrimaryAuthoritative(t *testing.T) {
	handler, plan, cleanup := strictReadinessHandler(t)
	defer cleanup()
	if err := handler.agentStore.CreateAgent("Saved Extra", nil); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"Rejected Primary","template_id":"strict-team","team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"lead","mode":"create","name":"Captain"}],"existing_agent_names":["Saved Extra"],"entry_agent_name":"Saved Extra"}`, plan.Revision)
	w, response := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusConflict || response["code"] != "team_readiness" {
		t.Fatalf("status/code = %d/%v, want 409/team_readiness: %s", w.Code, response["code"], w.Body.String())
	}
	assertNoReadinessSideEffects(t, handler, "Captain")
}

func TestWorkspaceTeamReadinessNoRoleBlueprintMayChooseSavedPrimary(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	tpl := projecttemplates.Template{ID: "no-role-team", Name: "No Role Team"}
	handler.projectTemplateResolver = func(templateID, templatePath string) (projecttemplates.Template, error) {
		if templateID == tpl.ID && strings.TrimSpace(templatePath) == "" {
			return tpl, nil
		}
		return projecttemplates.Template{}, fmt.Errorf("template not found")
	}
	for _, name := range []string{"First Extra", "Chosen Extra"} {
		if err := handler.agentStore.CreateAgent(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	plan := handler.buildTemplateAgentPlan(tpl)
	body := fmt.Sprintf(`{"name":"No Role Primary","template_id":%q,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[],"existing_agent_names":["First Extra","Chosen Extra"],"entry_agent_name":"Chosen Extra"}`, tpl.ID, plan.Revision)
	w, response := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	folder, _ := response["folder"].(map[string]any)
	workspaceID, _ := folder["id"].(string)
	current, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil || current == nil {
		t.Fatalf("read workspace: workspace=%#v err=%v", current, err)
	}
	entryName, _ := current.SharedData[workspaceEntryAgentNameKey].(string)
	if entryName != "Chosen Extra" {
		t.Fatalf("entry agent = %q, want Chosen Extra", entryName)
	}
}

func TestWorkspaceTeamReadinessAcceptsOnlyVerifiedInheritedHomeHolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	store := agentworkspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	handler.currentUserID = func(context.Context) (string, error) { return "local", nil }
	declaration := &agentworkspace.AssistantProgramDeclaration{
		SchemaVersion: agentworkspace.AssistantProgramSchemaVersion,
		ID:            "inherited-team",
		StationName:   "Inherited Home",
		Roles: []agentworkspace.AssistantProgramRoleSpec{{
			ID: "home-lead", Label: "Home Lead", Scope: agentworkspace.AssistantRoleScopeHome,
			Required: true, Primary: true, SystemPrompt: "Lead from Home.",
		}},
		Stages: []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
	}
	tpl := projecttemplates.Template{
		ID: "plugin:readiness:inherited", PluginOwner: &agentworkspace.PluginTemplateOwner{PluginID: "readiness"},
		AssistantProgram: declaration,
	}
	if err := handler.agentStore.CreateAgent("Home Lead", nil); err != nil {
		t.Fatal(err)
	}
	saved, _ := handler.agentStore.GetAgent("Home Lead")
	station := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Inherited Home"})
	station.Kind = "group"
	station.OwnerUserID = "local"
	station.AgentInstances = []agentworkspace.AgentInstance{{ID: "home-lead-instance", Name: "Home Lead", RoleID: "home-lead"}}
	station.SetAssistantProgramState(&agentworkspace.AssistantProgramState{
		SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion,
		Key: agentworkspace.AssistantProgramKey{
			OwnerUserID: "local", PluginID: "readiness", ProgramID: declaration.ID,
		},
		Declaration: declaration,
		Hired:       true,
		PrimaryName: "Home Lead",
		HomeBindings: agentworkspace.AssistantRoleBindingSet{Bindings: []agentworkspace.AssistantRoleBinding{{
			RoleID: "home-lead", AgentInstanceID: "home-lead-instance", AgentName: "Home Lead",
		}}},
	})
	if err := store.Save(station); err != nil {
		t.Fatal(err)
	}
	planWithoutSnapshot := handler.buildTemplateAgentPlanForOwner(tpl, "local")
	if planWithoutSnapshot.AssistantProgram.Roles[0].AgentName != "Home Lead" {
		t.Fatalf("plan did not project Home binding: %#v", planWithoutSnapshot.AssistantProgram.Roles)
	}
	if planWithoutSnapshot.AssistantProgram.StationWorkspaceSlug != station.FolderSlug {
		t.Fatalf("station recovery slug = %q, want %q", planWithoutSnapshot.AssistantProgram.StationWorkspaceSlug, station.FolderSlug)
	}
	req := &createWorkspaceRequest{
		TeamIntent:        json.RawMessage(fmt.Sprintf(`{"version":1,"mode":"staffed","plan_revision":%q}`, planWithoutSnapshot.Revision)),
		teamIntentPresent: true, roleStaffingPresent: true, RoleStaffing: []roleStaffingInput{},
	}
	if _, err := handler.validateWorkspaceTeamReadiness(context.Background(), req, tpl, tpl, true, "workspace"); err == nil {
		t.Fatal("a binding without a workspace snapshot was accepted")
	}
	if err := store.SaveWorkspaceAgent(station.ID, "Home Lead", saved); err != nil {
		t.Fatal(err)
	}
	plan := handler.buildTemplateAgentPlanForOwner(tpl, "local")
	req.TeamIntent = json.RawMessage(fmt.Sprintf(`{"version":1,"mode":"staffed","plan_revision":%q}`, plan.Revision))
	if _, err := handler.validateWorkspaceTeamReadiness(context.Background(), req, tpl, tpl, true, "workspace"); err != nil {
		t.Fatalf("verified inherited holder was rejected: %v", err)
	}
}

func TestWorkspaceTeamReadinessCreatesOnlyExplicitRequiredFill(t *testing.T) {
	handler, plan, cleanup := strictReadinessHandler(t)
	defer cleanup()

	body := fmt.Sprintf(`{"name":"Ready Team","template_id":"strict-team","team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"lead","mode":"create","name":"Captain"}]}`, plan.Revision)
	w, response := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if response["success"] != true {
		t.Fatalf("response success = %v", response["success"])
	}
	if _, found := handler.agentStore.GetAgent("Captain"); !found {
		t.Fatal("required role agent was not created")
	}
	if _, found := handler.agentStore.GetAgent("Optional Scout"); found {
		t.Fatal("optional blueprint proposal was created without an explicit fill")
	}
}

func TestWorkspaceTeamReadinessPersistsMixedCreateAndAssign(t *testing.T) {
	handler, plan, cleanup := strictReadinessHandler(t)
	defer cleanup()
	if err := handler.agentStore.CreateAgent("Saved Scout", nil); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"name":"Mixed Team","template_id":"strict-team","team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"lead","mode":"create","name":"Captain"},{"role_id":"optional-scout","mode":"assign","name":"Saved Scout"}]}`, plan.Revision)
	w, response := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	folder, _ := response["folder"].(map[string]any)
	workspaceID, _ := folder["id"].(string)
	current, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil || current == nil {
		t.Fatalf("read workspace: workspace=%#v err=%v", current, err)
	}
	entryName, _ := current.SharedData[workspaceEntryAgentNameKey].(string)
	if entryName != "Captain" || len(current.AgentInstances) != 2 {
		t.Fatalf("persisted team = entry %q, instances %#v", entryName, current.AgentInstances)
	}
	type persistedRole struct{ name, source string }
	roles := make(map[string]persistedRole, len(current.AgentInstances))
	for _, instance := range current.AgentInstances {
		roles[instance.RoleID] = persistedRole{name: instance.Name, source: instance.RoleSource}
	}
	if roles["lead"].name != "Captain" || roles["lead"].source != agentworkspace.RoleSourceCreated {
		t.Fatalf("lead binding = %#v", roles["lead"])
	}
	if roles["optional-scout"].name != "Saved Scout" || roles["optional-scout"].source != agentworkspace.RoleSourceAssigned {
		t.Fatalf("optional binding = %#v", roles["optional-scout"])
	}
	if _, found := handler.agentStore.GetAgent("Saved Scout"); !found {
		t.Fatal("assigned saved definition was deleted")
	}
}

func TestStrictAssistantProgramCompletionReflectsDurableBindings(t *testing.T) {
	tests := []struct {
		name             string
		applyCount       int
		staffingErr      error
		wantState        string
		wantApplied      int
		wantUnapplied    int
		wantProjectAgent int
	}{
		{name: "complete", applyCount: -1, wantState: "complete", wantApplied: 2, wantProjectAgent: 2},
		{name: "partial after durable required fill", applyCount: 1, staffingErr: fmt.Errorf("injected reviewer failure"), wantState: "incomplete", wantApplied: 1, wantUnapplied: 1, wantProjectAgent: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
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
			setTestAssistantRoleStaffer(handler, tt.applyCount, tt.staffingErr)
			plan := handler.buildTemplateAgentPlan(tpl)
			body := fmt.Sprintf(`{"name":"Assistant Completion","template_id":%q,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"lead","mode":"create","name":"Project Lead"},{"role_id":"reviewer","mode":"create","name":"Project Reviewer"}]}`, tpl.ID, plan.Revision)

			w, response := postCreateWorkspace(t, handler, body)
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
			}
			completion, ok := response["team_completion"].(map[string]any)
			if !ok || completion["state"] != tt.wantState {
				t.Fatalf("team completion = %#v, want state %q", response["team_completion"], tt.wantState)
			}
			applied, _ := completion["applied_role_ids"].([]any)
			unapplied, _ := completion["requested_unapplied_role_ids"].([]any)
			if len(applied) != tt.wantApplied || len(unapplied) != tt.wantUnapplied {
				t.Fatalf("completion roles = applied %#v, unapplied %#v", applied, unapplied)
			}
			folder, _ := response["folder"].(map[string]any)
			workspaceID, _ := folder["id"].(string)
			workspaceSlug, _ := folder["folder_slug"].(string)
			project, err := handler.workspaceTaskStore.Get(workspaceID)
			if err != nil || project == nil {
				t.Fatalf("read durable project: project=%#v err=%v", project, err)
			}
			if got := len(project.GetAgentInstances()); got != tt.wantProjectAgent {
				t.Fatalf("durable project agents = %d, want %d", got, tt.wantProjectAgent)
			}
			if tt.wantState == "complete" {
				if completion["team_staffed"] != true {
					t.Fatalf("complete team not staffed: %#v", completion)
				}
				action, _ := completion["action"].(map[string]any)
				if action["href"] != "" {
					t.Fatalf("complete team exposed recovery: %#v", action)
				}
			} else {
				action, _ := completion["action"].(map[string]any)
				if action["href"] != "/workspaces/"+workspaceSlug {
					t.Fatalf("partial recovery = %#v", action)
				}
			}
			workspaces, err := handler.store.ListWorkspaces(context.Background())
			projects := 0
			for _, workspace := range workspaces {
				if workspace.Kind != "group" {
					projects++
				}
			}
			if err != nil || projects != 1 {
				t.Fatalf("project count = %d (all workspaces %d), err=%v", projects, len(workspaces), err)
			}
		})
	}
}

func TestObserveAssistantTeamCompletionReportsDurablePartialState(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	store := agentworkspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	tpl := projecttemplates.Template{
		ID: "editorial-team", Name: "Editorial Team",
		Agents: []projecttemplates.AgentSpec{{Name: "Lead"}, {Name: "Writer"}},
	}
	handler.projectTemplateResolver = func(_, _ string) (projecttemplates.Template, error) {
		return tpl, nil
	}
	project := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Issue One"})
	project.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: tpl.ID})
	project.AgentInstances = []agentworkspace.AgentInstance{{
		ID: "lead-instance", Name: "Editorial Lead", RoleID: "lead",
		RoleSource: agentworkspace.RoleSourceCreated, EntryPoint: true,
	}}
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := handler.agentStore.CreateAgent("Editorial Lead", nil); err != nil {
		t.Fatal(err)
	}

	completion := handler.observeAssistantTeamCompletion(project.ID, project.FolderSlug, []roleStaffingInput{
		{RoleID: "lead", Mode: roleStaffingModeCreate, Name: "Editorial Lead"},
		{RoleID: "writer", Mode: roleStaffingModeCreate, Name: "Writer"},
	})
	if completion.State != "incomplete" || !completion.TeamStaffed {
		t.Fatalf("completion = %#v", completion)
	}
	if len(completion.AppliedRoleIDs) != 1 || completion.AppliedRoleIDs[0] != "lead" {
		t.Fatalf("applied roles = %#v", completion.AppliedRoleIDs)
	}
	if len(completion.MissingRequiredRoles) != 0 {
		t.Fatalf("missing required roles = %#v", completion.MissingRequiredRoles)
	}
	if len(completion.RequestedUnappliedRoleIDs) != 1 || completion.RequestedUnappliedRoleIDs[0] != "writer" {
		t.Fatalf("unapplied roles = %#v", completion.RequestedUnappliedRoleIDs)
	}
	if completion.WorkspaceID != project.ID || completion.Action.Href != "/workspaces/"+project.FolderSlug {
		t.Fatalf("recovery identity/action = %#v", completion)
	}
}

func TestStrictBlankAgentlessPersistsFillableAskOriRole(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	plan := handler.buildTemplateAgentPlan(blankWorkspaceTemplate())
	body := fmt.Sprintf(`{"name":"Persistent Empty","blank":true,"create_template_agents":false,"team_intent":{"version":1,"mode":"agentless","plan_revision":%q},"role_staffing":[]}`, plan.Revision)
	w, response := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	folder, ok := response["folder"].(map[string]any)
	if !ok {
		t.Fatalf("folder response = %#v", response["folder"])
	}
	workspaceID, _ := folder["id"].(string)
	current, err := handler.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		t.Fatalf("workspace read = %#v, err=%v", current, err)
	}
	roster := handler.buildWorkspaceRoster(current)
	if roster.TotalCount != 1 || len(roster.Roles) != 1 {
		t.Fatalf("roster = %#v", roster)
	}
	role := roster.Roles[0]
	if role.RoleID != "ask-ori" || !role.Required || !role.Primary || role.State != "empty" {
		t.Fatalf("Ask Ori role = %#v", role)
	}
}

func TestStrictBlankStaffingPersistsCreateAndAssign(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		agentName  string
		wantSource string
	}{
		{name: "create", mode: roleStaffingModeCreate, agentName: "Fresh Ask Ori", wantSource: agentworkspace.RoleSourceCreated},
		{name: "assign", mode: roleStaffingModeAssign, agentName: "Saved Ask Ori", wantSource: agentworkspace.RoleSourceAssigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			if tt.mode == roleStaffingModeAssign {
				if err := handler.agentStore.CreateAgent(tt.agentName, nil); err != nil {
					t.Fatal(err)
				}
			}
			plan := handler.buildTemplateAgentPlan(blankWorkspaceTemplate())
			body := fmt.Sprintf(`{"name":"Staffed Blank","blank":true,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[{"role_id":"ask-ori","mode":%q,"name":%q}]}`, plan.Revision, tt.mode, tt.agentName)
			w, response := postCreateWorkspace(t, handler, body)
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
			}
			folder, _ := response["folder"].(map[string]any)
			workspaceID, _ := folder["id"].(string)
			current, err := handler.store.GetWorkspace(context.Background(), workspaceID)
			if err != nil || current == nil {
				t.Fatalf("read workspace: workspace=%#v err=%v", current, err)
			}
			if len(current.AgentInstances) != 1 {
				t.Fatalf("instances = %#v, want one", current.AgentInstances)
			}
			instance := current.AgentInstances[0]
			if instance.Name != tt.agentName || instance.RoleID != "ask-ori" || instance.RoleSource != tt.wantSource {
				t.Fatalf("Ask Ori binding = %#v", instance)
			}
			entryName, _ := current.SharedData[workspaceEntryAgentNameKey].(string)
			if entryName != tt.agentName {
				t.Fatalf("entry agent = %q, want %q", entryName, tt.agentName)
			}
			if _, found := handler.agentStore.GetAgent(tt.agentName); !found {
				t.Fatalf("agent %q was not retained", tt.agentName)
			}
		})
	}
}

func TestBlankWorkspaceStrictModes(t *testing.T) {
	t.Run("staffed requires Ask Ori", func(t *testing.T) {
		handler, cleanup := createTestHandler(t)
		defer cleanup()
		plan := handler.buildTemplateAgentPlan(blankWorkspaceTemplate())
		body := fmt.Sprintf(`{"name":"Missing Ask Ori","blank":true,"team_intent":{"version":1,"mode":"staffed","plan_revision":%q},"role_staffing":[]}`, plan.Revision)
		w, response := postCreateWorkspace(t, handler, body)
		if w.Code != http.StatusConflict || response["code"] != "team_readiness" {
			t.Fatalf("status/code = %d/%v: %s", w.Code, response["code"], w.Body.String())
		}
		assertNoReadinessSideEffects(t, handler)
	})

	t.Run("agentless creates no fallback agent", func(t *testing.T) {
		handler, cleanup := createTestHandler(t)
		defer cleanup()
		plan := handler.buildTemplateAgentPlan(blankWorkspaceTemplate())
		body := fmt.Sprintf(`{"name":"Intentional Empty","blank":true,"create_template_agents":false,"team_intent":{"version":1,"mode":"agentless","plan_revision":%q},"role_staffing":[]}`, plan.Revision)
		w, _ := postCreateWorkspace(t, handler, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		if _, found := handler.agentStore.GetAgent(blankWorkspaceEntryAgentName); found {
			t.Fatal("agentless Blank created the Ask Ori fallback")
		}
		workspaces, err := handler.store.ListWorkspaces(context.Background())
		if err != nil || len(workspaces) != 1 {
			t.Fatalf("workspaces = %d, err=%v", len(workspaces), err)
		}
		if got := workspaces[0].EntryAgentName; got != "" {
			t.Fatalf("entry agent = %q, want empty", got)
		}
	})
}
