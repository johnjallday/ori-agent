package setupjourney

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type staffingGrantStub struct {
	available map[string]bool
	granted   map[string]map[string]bool
}

func (s *staffingGrantStub) Available(name string) bool { return s.available[name] }
func (s *staffingGrantStub) Grant(agentName, skillName string) error {
	if s.granted[agentName] == nil {
		s.granted[agentName] = make(map[string]bool)
	}
	s.granted[agentName][skillName] = true
	return nil
}
func (s *staffingGrantStub) Revoke(agentName, skillName string) error {
	delete(s.granted[agentName], skillName)
	return nil
}

func staffingFixture(t *testing.T) (*AssistantStaffingAdapter, workspace.Store, ReadScope, *staffingGrantStub) {
	t.Helper()
	workspaces := workspace.NewInMemoryStore()
	profiles, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{Model: "gpt-4o-mini", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	declaration := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion,
		ID:            "studio-guide", StationName: "Studio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff assistants",
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "home_guide", Label: "Home Guide", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "home-only prompt"},
			{ID: "project_lead", Label: "Project Lead", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "project-lead-only prompt", Skills: []string{"project-skill"}},
			{ID: "project_reviewer", Label: "Project Reviewer", Scope: workspace.AssistantRoleScopeProject, Required: true, Role: "specialist", Type: "general", SystemPrompt: "project-review-only prompt"},
		},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Find stable preferences."},
	}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "First Project"})
	project.OwnerUserID = "owner-1"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		PluginOwner:      &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: declaration,
	})
	if err := workspaces.Save(project); err != nil {
		t.Fatal(err)
	}
	station, _, err := workspace.NewAssistantProgramStore(workspaces).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	grants := &staffingGrantStub{available: map[string]bool{"project-skill": true}, granted: make(map[string]map[string]bool)}
	adapter := NewAssistantStaffingAdapter(workspaces, profiles, grants,
		func() (string, string) { return "openai", "gpt-4o-mini" },
		func(provider, model string) error {
			if provider != "openai" || model != "gpt-4o-mini" {
				return ErrInvalid
			}
			return nil
		})
	scope := ReadScope{
		OwnerUserID: "owner-1", ExpectedAssistantProgramID: declaration.ID,
		HomeWorkspaceID: station.ID, ProjectWorkspaceID: project.ID, SelectedModeID: "file_only",
	}
	return adapter, workspaces, scope, grants
}

func TestAssistantStaffingAdapter_SeparatelyReviewsAndCreatesScopedProfiles(t *testing.T) {
	adapter, workspaces, scope, grants := staffingFixture(t)
	before, err := adapter.Read(context.Background(), scope)
	if err != nil || before.Complete || !containsAction(before.AvailableActions, ActionReviewHomeStaffing) || !containsAction(before.AvailableActions, ActionReviewProjectStaffing) {
		t.Fatalf("initial staffing = %#v, %v", before, err)
	}

	homeInput := []byte(`{"roles":[{"role_id":"home_guide","name":"June Home Guide"}]}`)
	homeReview, err := adapter.Review(context.Background(), scope, ActionReviewHomeStaffing, homeInput)
	if err != nil {
		t.Fatal(err)
	}
	if homeReview.CommitAction != ActionAddHomeStaffing || homeReview.Staffing == nil || len(homeReview.Staffing.Scopes) != 1 {
		t.Fatalf("Home review = %#v", homeReview)
	}
	homeRole := homeReview.Staffing.Scopes[0].Roles[0]
	if homeRole.ProfileName != "June Home Guide" || homeRole.Provider != "openai" || homeRole.Model != "gpt-4o-mini" || len(homeRole.ToolGrants) != 0 {
		t.Fatalf("Home role disclosure = %#v", homeRole)
	}
	encodedReview := string(mustJSON(t, homeReview.Staffing))
	if strings.Contains(encodedReview, "home-only prompt") || strings.Contains(encodedReview, "project-lead-only prompt") {
		t.Fatalf("staffing disclosure leaked prompts: %s", encodedReview)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionAddHomeStaffing, homeInput, homeReview); err != nil {
		t.Fatal(err)
	}

	afterHome, err := adapter.Read(context.Background(), scope)
	if err != nil || afterHome.Complete || !afterHome.Staffing.Scopes[0].RequiredComplete || afterHome.Staffing.Scopes[1].RequiredComplete {
		t.Fatalf("after Home staffing = %#v, %v", afterHome, err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(station.GetAgentInstances()) != 1 || len(project.GetAgentInstances()) != 0 || station.EntryAgentName() != "June Home Guide" {
		t.Fatalf("scope topology after Home staffing: Home=%#v project=%#v", station.GetAgentInstances(), project.GetAgentInstances())
	}
	homeProfile, found, err := workspaces.GetWorkspaceAgent(station.ID, "June Home Guide")
	if err != nil || !found || homeProfile.Settings.SystemPrompt != "home-only prompt" {
		t.Fatalf("Home snapshot = %#v, %v, %v", homeProfile, found, err)
	}
	if _, found, _ := workspaces.GetWorkspaceAgent(project.ID, "June Home Guide"); found {
		t.Fatal("Home profile snapshot bled into project")
	}

	projectInput := []byte(`{"roles":[{"role_id":"project_reviewer","name":"Iris Reviewer","provider":"openai","model":"gpt-4o-mini"},{"role_id":"project_lead","name":"Alex Project Lead","provider":"openai","model":"gpt-4o-mini"}]}`)
	projectReview, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, projectInput)
	if err != nil {
		t.Fatal(err)
	}
	roles := projectReview.Staffing.Scopes[0].Roles
	if len(roles) != 2 || roles[0].RoleID != "project_lead" || roles[0].ProfileName != "Alex Project Lead" || len(roles[0].ToolGrants) != 1 {
		t.Fatalf("project review order/details = %#v", roles)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionAddProjectStaffing, projectInput, projectReview); err != nil {
		t.Fatal(err)
	}

	afterProject, err := adapter.Read(context.Background(), scope)
	if err != nil || !afterProject.Complete || !afterProject.Staffing.Scopes[0].RequiredComplete || !afterProject.Staffing.Scopes[1].RequiredComplete {
		t.Fatalf("completed staffing = %#v, %v", afterProject, err)
	}
	station, _ = workspaces.Get(scope.HomeWorkspaceID)
	project, _ = workspaces.Get(scope.ProjectWorkspaceID)
	if len(station.GetAgentInstances()) != 1 || len(project.GetAgentInstances()) != 2 || project.EntryAgentName() != "Alex Project Lead" {
		t.Fatalf("final scoped instances: Home=%#v project=%#v entry=%q", station.GetAgentInstances(), project.GetAgentInstances(), project.EntryAgentName())
	}
	if station.GetAssistantProgramState().HomeBindings.StateRevision != 1 || project.GetAssistantProjectLink().ProjectBindings.StateRevision != 1 {
		t.Fatalf("binding revisions: Home=%d project=%d", station.GetAssistantProgramState().HomeBindings.StateRevision, project.GetAssistantProjectLink().ProjectBindings.StateRevision)
	}
	leadProfile, found, _ := workspaces.GetWorkspaceAgent(project.ID, "Alex Project Lead")
	if !found || leadProfile.Settings.SystemPrompt != "project-lead-only prompt" || !grants.granted["Alex Project Lead"]["project-skill"] {
		t.Fatalf("project lead snapshot/grants = %#v grants=%#v", leadProfile, grants.granted)
	}
	if _, found, _ := workspaces.GetWorkspaceAgent(station.ID, "Alex Project Lead"); found {
		t.Fatal("project profile snapshot bled into Home")
	}

	communicator := agentcomm.NewCommunicator(workspaces)
	if _, err := communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: station.ID, From: "June Home Guide", To: "Iris Reviewer", Description: "Cross the scope boundary",
	}); err == nil {
		t.Fatal("Home manager delegated directly to a project specialist")
	}
	if _, err := communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: project.ID, From: "Alex Project Lead", To: "Iris Reviewer", Description: "Review this project",
	}); err != nil {
		t.Fatalf("project-local delegation failed: %v", err)
	}
	if _, err := communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: project.ID, From: "Alex Project Lead", To: "June Home Guide", Description: "Cross back into Home",
	}); err == nil {
		t.Fatal("project coordinator delegated directly to a Home role")
	}

	firstIDs := []string{project.GetAgentInstances()[0].ID, project.GetAgentInstances()[1].ID}
	second := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Second Project"})
	second.OwnerUserID = "owner-1"
	second.SetTemplateProvenance(project.GetTemplateProvenance())
	if err := workspaces.Save(second); err != nil {
		t.Fatal(err)
	}
	if secondStation, created, err := workspace.NewAssistantProgramStore(workspaces).EnsureProjectStation(second.ID); err != nil || created || secondStation.ID != station.ID {
		t.Fatalf("second project link = (%#v, %v, %v)", secondStation, created, err)
	}
	secondScope := scope
	secondScope.ProjectWorkspaceID = second.ID
	secondRead, err := adapter.Read(context.Background(), secondScope)
	if err != nil || secondRead.Staffing == nil || !secondRead.Staffing.Scopes[0].RequiredComplete || secondRead.Staffing.Scopes[1].RequiredComplete {
		t.Fatalf("second project staffing state = %#v, %v", secondRead, err)
	}
	secondInput := []byte(`{"roles":[{"role_id":"project_lead","name":"Morgan Second Lead"},{"role_id":"project_reviewer","name":"Sage Second Reviewer"}]}`)
	secondReview, err := adapter.Review(context.Background(), secondScope, ActionReviewProjectStaffing, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Commit(context.Background(), secondScope, ActionAddProjectStaffing, secondInput, secondReview); err != nil {
		t.Fatal(err)
	}
	storedFirst, _ := workspaces.Get(project.ID)
	storedSecond, _ := workspaces.Get(second.ID)
	if storedFirst.GetAgentInstances()[0].ID != firstIDs[0] || storedFirst.GetAgentInstances()[1].ID != firstIDs[1] || len(storedSecond.GetAgentInstances()) != 2 {
		t.Fatalf("second staffing changed first identities: first=%#v second=%#v", storedFirst.GetAgentInstances(), storedSecond.GetAgentInstances())
	}
	if _, err := communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: project.ID, From: "Alex Project Lead", To: "Sage Second Reviewer", Description: "Address a sibling",
	}); err == nil {
		t.Fatal("first project coordinator delegated to a sibling project role")
	}
}

func TestAssistantStaffingAdapter_ProjectReviewPreservesValidPartialBinding(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	if err := adapter.profiles.CreateAgent("Existing Lead", &agentstore.CreateAgentConfig{
		Type: "tool-calling", Role: "orchestrator", LLMProvider: "openai", Model: "gpt-4o-mini", SystemPrompt: "project-lead-only prompt",
	}); err != nil {
		t.Fatal(err)
	}
	profile, found := adapter.profiles.GetAgent("Existing Lead")
	if !found || profile == nil {
		t.Fatal("partial profile missing")
	}
	if err := workspaces.SaveWorkspaceAgent(scope.ProjectWorkspaceID, "Existing Lead", profile); err != nil {
		t.Fatal(err)
	}
	instance := workspace.AgentInstance{ID: "existing-lead-id", Name: "Existing Lead", Role: "Project Lead", EntryPoint: true}
	if err := workspaces.Update(scope.ProjectWorkspaceID, func(current *workspace.Workspace) error {
		current.AgentInstances = append(current.GetAgentInstances(), instance)
		link := current.GetAssistantProjectLink()
		link.ProjectBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{RoleID: "project_lead", AgentInstanceID: instance.ID, AgentName: instance.Name}}}
		current.SetAssistantProjectLink(link)
		return current.SetEntryAgentName(instance.Name)
	}); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"roles":[{"role_id":"project_reviewer","name":"New Reviewer"}]}`)
	review, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Staffing.Scopes[0].Roles) != 1 || review.Staffing.Scopes[0].Roles[0].RoleID != "project_reviewer" || review.Staffing.Scopes[0].BindingRevision != 1 {
		t.Fatalf("partial review = %#v", review.Staffing)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionAddProjectStaffing, input, review); err != nil {
		t.Fatal(err)
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	bindings := project.GetAssistantProjectLink().ProjectBindings
	if bindings.StateRevision != 2 || len(bindings.Bindings) != 2 || len(project.GetAgentInstances()) != 2 {
		t.Fatalf("completed partial project binding = %#v instances=%#v", bindings, project.GetAgentInstances())
	}
}

func TestAssistantStaffingAdapter_NoModelStillCreatesDeterministicHomeBinding(t *testing.T) {
	_, workspaces, scope, grants := staffingFixture(t)
	profiles, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "no-model-agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewAssistantStaffingAdapter(workspaces, profiles, grants,
		func() (string, string) { return "", "" },
		func(provider, model string) error { return ErrInvalid })
	input := []byte(`{"roles":[{"role_id":"home_guide","name":"Offline Home Guide"}]}`)
	review, err := adapter.Review(context.Background(), scope, ActionReviewHomeStaffing, input)
	if err != nil {
		t.Fatal(err)
	}
	target := review.Staffing.Scopes[0]
	if target.ModelsReady || target.Roles[0].ChatAvailable || target.Roles[0].Provider != "" || target.Roles[0].Model != "" {
		t.Fatalf("no-model disclosure = %#v", target)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionAddHomeStaffing, input, review); err != nil {
		t.Fatal(err)
	}
	read, err := adapter.Read(context.Background(), scope)
	if err != nil || !read.Staffing.Scopes[0].RequiredComplete || read.Staffing.Scopes[0].ModelsReady {
		t.Fatalf("no-model canonical read = %#v, %v", read, err)
	}
	station, _ := workspaces.Get(scope.HomeWorkspaceID)
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	state := station.GetAssistantProgramState()
	if state.Reflection.ScheduleTaskID != "" || len(station.Tasks) != 0 || len(project.Tasks) != 0 || project.GetRuntimeState() != nil || len(project.GetAgentInstances()) != 0 {
		t.Fatalf("staffing caused unrelated consequences: Home=%#v project=%#v", station, project)
	}
}

func TestAssistantStaffingAdapter_ReviewRejectsCrossScopeNamesAndUnavailableTools(t *testing.T) {
	adapter, _, scope, grants := staffingFixture(t)
	wrongScope := []byte(`{"roles":[{"role_id":"project_lead","name":"Wrong Scope"}]}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewHomeStaffing, wrongScope); err == nil {
		t.Fatal("cross-scope Home review succeeded")
	}
	grants.available["project-skill"] = false
	projectInput := []byte(`{"roles":[{"role_id":"project_lead","name":"Lead"},{"role_id":"project_reviewer","name":"Reviewer"}]}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, projectInput); err == nil {
		t.Fatal("review succeeded with unavailable declared tool grant")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
