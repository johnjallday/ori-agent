package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func getGroupTemplateStatus(t *testing.T, handler *Handler, workspaceID string) (int, groupTemplateStatus) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/group-template", nil)
	request.SetPathValue("workspaceID", workspaceID)
	response := httptest.NewRecorder()
	handler.GetGroupTemplateStatus(response, request)
	var body struct {
		GroupTemplate groupTemplateStatus `json:"group_template"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response.Code, body.GroupTemplate
}

func TestGroupTemplateStatus_OrdinaryGroupsStayGeneralWithoutInference(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	impostor := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Neutral Program Home"})
	impostor.Kind = "group"
	impostor.OwnerUserID = "local"
	project := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Plain Project"})
	for _, ws := range []*agentworkspace.Workspace{impostor, project} {
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}

	code, status := getGroupTemplateStatus(t, handler, impostor.ID)
	if code != http.StatusOK || status.Kind != projecttemplates.GroupTemplateKindOrdinary || status.Template == nil ||
		status.Template.Name != "General" || status.Provider != nil || status.Team != nil || status.Integration != nil {
		t.Fatalf("same-named ordinary group status = %d %+v", code, status)
	}
	if groupTemplateSummary(impostor) != nil {
		t.Fatal("an ordinary group must not receive a template summary")
	}
	if code, _ := getGroupTemplateStatus(t, handler, project.ID); code != http.StatusBadRequest {
		t.Fatalf("non-group status code = %d, want 400", code)
	}
}

func TestGroupTemplateStatus_SeparatesGroupTeamAndIntegrationFacts(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	enabled := true
	installed := true
	base := handler.installedPluginLister
	handler.SetInstalledPluginLister(installedPluginListerFunc(func() ([]plugin.InstalledPlugin, error) {
		if !installed {
			return nil, nil
		}
		listed, err := base.List()
		plugins := append([]plugin.InstalledPlugin(nil), listed...)
		for index := range plugins {
			plugins[index].Enabled = enabled
		}
		return plugins, err
	}))
	handler.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		return []GroupTemplateCatalogEntry{{Template: template, Readiness: handler.revalidateBlueprintReadiness(template), Active: enabled && installed}}, false, nil
	})
	homeID := preparePolicyHome(t, handler)
	before, _ := store.Get(homeID)
	beforeRevision := before.GetAssistantProgramState().StateRevision

	code, status := getGroupTemplateStatus(t, handler, homeID)
	if code != http.StatusOK || status.Kind != projecttemplates.GroupTemplateKindManaged || status.Group.State != "created" ||
		status.Template == nil || status.Template.Name != "Neutral Program Home" || status.Provider == nil || status.Provider.PluginID != "neutral" {
		t.Fatalf("managed status = %d %+v", code, status)
	}
	if status.Team == nil || status.Team.State != groupTemplateTeamIncomplete || !slicesContainString(status.Team.Actions, groupTemplateActionOpenRoles) ||
		len(status.Team.OptionalHomeRoles) != 1 || status.Team.OptionalHomeRoles[0].RoleID != "home-addon" ||
		len(status.Team.ProjectRoles) != 1 || status.Team.ProjectRoles[0] != "Project Lead" {
		t.Fatalf("unstaffed team = %+v", status.Team)
	}
	requirePlanCounts(t, status.Team.RequiredHomeRoles, 0, 1)
	if status.Integration == nil || status.Integration.State != groupTemplateIntegrationAvailable {
		t.Fatalf("available integration = %+v", status.Integration)
	}
	if status.Template.CreatedFromTemplate || status.Template.GroupTemplateID != "" {
		t.Fatalf("a Home prepared by another path was given template provenance: %+v", status.Template)
	}

	if err := handler.agentStore.CreateAgent("Verified Home Lead", nil); err != nil {
		t.Fatal(err)
	}
	saved, _ := handler.agentStore.GetAgent("Verified Home Lead")
	if err := store.Update(homeID, func(home *agentworkspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings = agentworkspace.AssistantRoleBindingSet{StateRevision: 2, Bindings: []agentworkspace.AssistantRoleBinding{{
			RoleID: "home-lead", AgentInstanceID: "home-lead-instance", AgentName: "Verified Home Lead",
		}}}
		home.AgentInstances = []agentworkspace.AgentInstance{{ID: "home-lead-instance", Name: "Verified Home Lead", RoleID: "home-lead"}}
		home.Name = "Renamed Program Home"
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceAgent(homeID, "Verified Home Lead", saved); err != nil {
		t.Fatal(err)
	}
	stateRevision := func() int64 {
		current, _ := store.Get(homeID)
		return current.GetAssistantProgramState().StateRevision
	}
	staffedRevision := stateRevision()
	_, staffed := getGroupTemplateStatus(t, handler, homeID)
	if staffed.Name != "Renamed Program Home" || staffed.Template.Name != "Neutral Program Home" || staffed.Team.State != groupTemplateTeamReady ||
		len(staffed.Team.Actions) != 0 || staffed.Integration.State != groupTemplateIntegrationAvailable {
		t.Fatalf("staffed renamed status = %+v team=%+v integration=%+v", staffed, staffed.Team, staffed.Integration)
	}
	requirePlanCounts(t, staffed.Team.RequiredHomeRoles, 1, 0)

	enabled = false
	_, disabled := getGroupTemplateStatus(t, handler, homeID)
	if disabled.Team.State != groupTemplateTeamReady || disabled.Integration.State != groupTemplateIntegrationUnavailable ||
		disabled.Integration.Reason != "plugin_enable_required" || disabled.Template.Name != "Neutral Program Home" || disabled.Provider.PluginID != "neutral" {
		t.Fatalf("disabled source status = team %+v integration %+v", disabled.Team, disabled.Integration)
	}

	installed = false
	_, removed := getGroupTemplateStatus(t, handler, homeID)
	if removed.Team.State != groupTemplateTeamReady || removed.Integration.State != groupTemplateIntegrationUnavailable ||
		removed.Integration.Reason != "plugin_install_required" || removed.Template.Name != "Neutral Program Home" {
		t.Fatalf("uninstalled source status = team %+v integration %+v", removed.Team, removed.Integration)
	}

	installed, enabled = true, true
	_, reinstalled := getGroupTemplateStatus(t, handler, homeID)
	if reinstalled.Integration.State != groupTemplateIntegrationAvailable || reinstalled.Team.State != groupTemplateTeamReady {
		t.Fatalf("reinstalled source status = team %+v integration %+v", reinstalled.Team, reinstalled.Integration)
	}

	if got := stateRevision(); got != staffedRevision || beforeRevision == 0 {
		t.Fatalf("status reads mutated program state: revision %d -> %d", staffedRevision, got)
	}
	current, _ := store.Get(homeID)
	summary := groupTemplateSummary(current)
	if summary == nil || summary.Name != "Neutral Program Home" || summary.PluginID != "neutral" || summary.Kind != "managed_home" {
		t.Fatalf("list summary = %+v", summary)
	}
}

func TestGroupTemplateStatus_ConflictingSourcesAndLegacyHomesNeedRecovery(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	conflict := false
	handler.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		entries := []GroupTemplateCatalogEntry{{Template: template, Readiness: handler.revalidateBlueprintReadiness(template), Active: true}}
		if conflict {
			second := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
			second.ID = "plugin:neutral:second"
			second.AssistantProgram.StationDescription = "A different Home definition."
			entries = append(entries, GroupTemplateCatalogEntry{Template: second, Readiness: handler.revalidateBlueprintReadiness(second), Active: true})
		}
		return entries, false, nil
	})
	homeID := preparePolicyHome(t, handler)

	conflict = true
	_, conflicted := getGroupTemplateStatus(t, handler, homeID)
	if conflicted.Integration == nil || conflicted.Integration.State != groupTemplateIntegrationUnavailable ||
		conflicted.Integration.Reason != "home_declaration_conflict" || conflicted.Team == nil || conflicted.Team.State != groupTemplateTeamIncomplete {
		t.Fatalf("conflicting sources status = team %+v integration %+v", conflicted.Team, conflicted.Integration)
	}

	conflict = false
	if err := store.Update(homeID, func(home *agentworkspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.SchemaVersion = agentworkspace.AssistantProgramLegacyStateSchemaVersion
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, legacy := getGroupTemplateStatus(t, handler, homeID)
	if legacy.Kind != projecttemplates.GroupTemplateKindManaged || legacy.Team == nil || legacy.Team.State != groupTemplateTeamMigrationRequired ||
		legacy.Integration == nil || legacy.Integration.State != groupTemplateIntegrationUnavailable || legacy.Integration.Reason != "home_incompatible" {
		t.Fatalf("legacy Home status = team %+v integration %+v", legacy.Team, legacy.Integration)
	}
}

type installedPluginListerFunc func() ([]plugin.InstalledPlugin, error)

func (fn installedPluginListerFunc) List() ([]plugin.InstalledPlugin, error) { return fn() }
