package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type listUnavailableWorkspaceStore struct {
	agentworkspace.Store
}

func (listUnavailableWorkspaceStore) List() ([]string, error) {
	return nil, errors.New("workspace storage unavailable")
}

func groupedPlanTemplate(policy projecttemplates.GroupPolicy) projecttemplates.Template {
	template := policyTemplate(policy)
	template.AssistantProgram.Roles = []agentworkspace.AssistantProgramRoleSpec{
		{
			ID: "home-lead", Label: "Home Lead", Scope: agentworkspace.AssistantRoleScopeHome,
			Required: true, Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "Coordinate the Home.",
		},
		{
			ID: "project-lead", Label: "Project Lead", Scope: agentworkspace.AssistantRoleScopeProject,
			Required: true, Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "Coordinate the project.",
		},
		{
			ID: "home-addon", Label: "Home Add-on", Scope: agentworkspace.AssistantRoleScopeHome,
			CapabilityID: "archive", Role: "specialist", Type: "general", SystemPrompt: "Organize reviewed records.",
		},
	}
	template.AssistantProgram.Stages = []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}}
	return template
}

func installPlanAgentStore(t *testing.T, handler *Handler) {
	t.Helper()
	store, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetAgentStore(store)
}

func postTemplateGroupPlan(t *testing.T, handler *Handler, templateID, composition string) (int, templateAgentPlan, map[string]any) {
	t.Helper()
	payload := map[string]any{"template_id": templateID}
	if composition != "" {
		payload["group_composition"] = composition
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.handleTemplateAgentPlan(response, request)
	var plan templateAgentPlan
	_ = json.Unmarshal(response.Body.Bytes(), &plan)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plan response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, plan, body
}

func requirePlanCounts(t *testing.T, progress templateGroupRequirementRequiredRolesPlan, filled, missing int) {
	t.Helper()
	if progress.Required == nil || progress.Filled == nil || progress.Missing == nil {
		t.Fatalf("progress omitted known counts: %+v", progress)
	}
	if *progress.Required != 1 || *progress.Filled != filled || *progress.Missing != missing {
		t.Fatalf("progress = %d/%d/%d, want 1/%d/%d", *progress.Required, *progress.Filled, *progress.Missing, filled, missing)
	}
}

func TestTemplateGroupRequirementPlan_OmittedForLegacyTemplate(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	template := projecttemplates.Template{ID: "legacy", Name: "Legacy", Builtin: true}
	handler.SetProjectTemplateResolver(func(id, path string) (projecttemplates.Template, error) {
		if id == template.ID && path == "" {
			return template, nil
		}
		return projecttemplates.Template{}, projecttemplates.ErrTemplateNotFound
	})
	code, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	if code != http.StatusOK || plan.GroupRequirement != nil {
		t.Fatalf("status/plan = %d/%+v, want 200 with no group projection", code, plan.GroupRequirement)
	}
}

func TestTemplateGroupRequirementPlan_AbsentHomeIsInertAndDistinguishesProposal(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)

	code, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "grouped")
	if code != http.StatusOK || plan.GroupRequirement == nil {
		t.Fatalf("status/plan = %d/%+v", code, plan.GroupRequirement)
	}
	group := plan.GroupRequirement
	if group.Version != 1 || len(group.SourceRevision) != 64 || group.State != grouprequirements.StateHomeCreationReviewRequired ||
		group.Policy != projecttemplates.GroupPolicyRequired || group.SelectedComposition != grouprequirements.CompositionGrouped {
		t.Fatalf("group projection = %+v", group)
	}
	if group.Home == nil || group.Home.Exists || group.Home.ProposedName != "Neutral Program Home" || group.Home.Name != "" || group.Home.WorkspaceID != "" {
		t.Fatalf("absent Home was not projected as a proposal: %+v", group.Home)
	}
	if group.RequiredHomeRoles.Verification != templateGroupRoleVerificationAbsent || len(group.RequiredHomeRoles.Roles) != 1 || group.RequiredHomeRoles.Roles[0].RoleID != "home-lead" {
		t.Fatalf("required Home roles = %+v", group.RequiredHomeRoles)
	}
	requirePlanCounts(t, group.RequiredHomeRoles, 0, 1)
	if !slicesContainAction(group.Actions, grouprequirements.ActionReviewCreateHome) {
		t.Fatalf("actions = %v, want review_create_home", group.Actions)
	}
	ids, err := workspaceStore.List()
	if err != nil || len(ids) != 0 {
		t.Fatalf("passive plan mutated workspaces: ids=%v err=%v", ids, err)
	}
}

func TestTemplateGroupRequirementPlan_ExistingOnlyNeverOffersHomeCreation(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	template.GroupRequirement.MissingHome = projecttemplates.MissingHomeExistingOnly
	template.GroupRequirement.DefaultHomeName = ""
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)

	code, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "grouped")
	group := plan.GroupRequirement
	if code != http.StatusOK || group == nil || group.State != grouprequirements.StateHomeRequired {
		t.Fatalf("existing-only projection = status %d, %+v", code, group)
	}
	if group.Home == nil || group.Home.Exists || group.Home.ProposedName != "" {
		t.Fatalf("existing-only projection invented a proposed Home: %+v", group.Home)
	}
	if slicesContainAction(group.Actions, grouprequirements.ActionReviewCreateHome) {
		t.Fatalf("existing-only actions offered forbidden Home creation: %v", group.Actions)
	}
	ids, err := workspaceStore.List()
	if err != nil || len(ids) != 0 {
		t.Fatalf("existing-only plan mutated workspaces: ids=%v err=%v", ids, err)
	}
}

func TestTemplateGroupRequirementPlan_SameNamedOrdinaryAndWrongSourceHomesDoNotSatisfy(t *testing.T) {
	for _, scenario := range []struct {
		name string
		home *agentworkspace.Workspace
	}{
		{name: "same-named ordinary group", home: func() *agentworkspace.Workspace {
			home := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Neutral Program Home"})
			home.Kind = "group"
			home.OwnerUserID = "local"
			return home
		}()},
		{name: "wrong program source", home: func() *agentworkspace.Workspace {
			home := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Neutral Program Home"})
			home.Kind = "group"
			home.OwnerUserID = "local"
			home.SetAssistantProgramState(&agentworkspace.AssistantProgramState{
				SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion,
				Key:           agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "other", ProgramID: "neutral-program"},
			})
			return home
		}()},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
			handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
			defer cleanup()
			installPlanAgentStore(t, handler)
			if err := workspaceStore.Save(scenario.home); err != nil {
				t.Fatal(err)
			}
			code, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
			if code != http.StatusOK || plan.GroupRequirement == nil || plan.GroupRequirement.State != grouprequirements.StateHomeCreationReviewRequired {
				t.Fatalf("wrong Home satisfied exact identity: status=%d plan=%+v", code, plan.GroupRequirement)
			}
			if plan.GroupRequirement.Home == nil || plan.GroupRequirement.Home.Exists || plan.GroupRequirement.Home.WorkspaceID != "" {
				t.Fatalf("wrong Home identity leaked as exact: %+v", plan.GroupRequirement.Home)
			}
		})
	}
}

func TestTemplateGroupRequirementPlan_UsesRenamedExactHomeAndVerifiedHolder(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	homeID := preparePolicyHome(t, handler)
	if err := workspaceStore.Update(homeID, func(home *agentworkspace.Workspace) error {
		home.Name = "Renamed Research Home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	_, emptyPlan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	group := emptyPlan.GroupRequirement
	if group == nil || group.State != grouprequirements.StateReadyGrouped || group.Home == nil || !group.Home.Exists ||
		group.Home.WorkspaceID != homeID || group.Home.Name != "Renamed Research Home" || group.Home.ProposedName != "" || group.Home.FolderSlug == "" {
		t.Fatalf("exact renamed Home projection = %+v", group)
	}
	if group.RequiredHomeRoles.Verification != templateGroupRoleVerificationVerified || !slicesContainAction(group.Actions, grouprequirements.ActionOpenGroupRoles) {
		t.Fatalf("empty exact Home recovery = %+v", group)
	}
	requirePlanCounts(t, group.RequiredHomeRoles, 0, 1)

	if err := handler.agentStore.CreateAgent("Verified Home Lead", nil); err != nil {
		t.Fatal(err)
	}
	saved, _ := handler.agentStore.GetAgent("Verified Home Lead")
	if err := workspaceStore.Update(homeID, func(home *agentworkspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Hired = true
		state.HomeBindings = agentworkspace.AssistantRoleBindingSet{StateRevision: 2, Bindings: []agentworkspace.AssistantRoleBinding{{
			RoleID: "home-lead", AgentInstanceID: "home-lead-instance", AgentName: "Verified Home Lead",
		}}}
		home.AgentInstances = []agentworkspace.AgentInstance{{
			ID: "home-lead-instance", Name: "Verified Home Lead", RoleID: "home-lead", RoleSource: agentworkspace.RoleSourceCreated,
		}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := workspaceStore.SaveWorkspaceAgent(homeID, "Verified Home Lead", saved); err != nil {
		t.Fatal(err)
	}

	_, filledPlan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	filled := filledPlan.GroupRequirement
	if filled == nil || filled.RequiredHomeRoles.Verification != templateGroupRoleVerificationVerified {
		t.Fatalf("filled plan = %+v", filled)
	}
	requirePlanCounts(t, filled.RequiredHomeRoles, 1, 0)
	role := filled.RequiredHomeRoles.Roles[0]
	if role.State != "filled" || role.Agent == nil || role.Agent.Name != "Verified Home Lead" {
		t.Fatalf("verified role = %+v", role)
	}
	if slicesContainAction(filled.Actions, grouprequirements.ActionOpenGroupRoles) {
		t.Fatalf("staffed Home retained redundant setup action: %v", filled.Actions)
	}
	if filledPlan.Revision == emptyPlan.Revision {
		t.Fatal("verified holder change did not invalidate the team plan revision")
	}
}

func TestTemplateGroupRequirementPlan_StaleHolderProjectsEmpty(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	homeID := preparePolicyHome(t, handler)
	if err := workspaceStore.Update(homeID, func(home *agentworkspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Hired = true
		state.HomeBindings = agentworkspace.AssistantRoleBindingSet{Bindings: []agentworkspace.AssistantRoleBinding{{
			RoleID: "home-lead", AgentInstanceID: "stale-instance", AgentName: "Deleted Lead",
		}}}
		home.AgentInstances = []agentworkspace.AgentInstance{{ID: "stale-instance", Name: "Deleted Lead", RoleID: "home-lead"}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	_, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	group := plan.GroupRequirement
	if group == nil || group.RequiredHomeRoles.Roles[0].State != "empty" || group.RequiredHomeRoles.Roles[0].Agent != nil {
		t.Fatalf("stale holder was projected filled: %+v", group)
	}
	requirePlanCounts(t, group.RequiredHomeRoles, 0, 1)
}

func TestTemplateGroupRequirementPlan_FailsClosedForOwnerStorageAndAmbiguity(t *testing.T) {
	t.Run("current owner unavailable", func(t *testing.T) {
		template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
		handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
		defer cleanup()
		installPlanAgentStore(t, handler)
		handler.currentUserID = func(context.Context) (string, error) { return "", errors.New("owner unavailable") }
		_, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
		group := plan.GroupRequirement
		if group == nil || group.State != grouprequirements.StateSourceUnavailable || group.Home != nil ||
			group.RequiredHomeRoles.Verification != templateGroupRoleVerificationUnavailable || group.RequiredHomeRoles.Required != nil {
			t.Fatalf("owner failure did not fail closed: %+v", group)
		}
		ids, _ := workspaceStore.List()
		if len(ids) != 0 {
			t.Fatalf("owner failure mutated workspaces: %v", ids)
		}
	})

	t.Run("workspace storage unavailable", func(t *testing.T) {
		template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
		handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
		defer cleanup()
		installPlanAgentStore(t, handler)
		unavailable := listUnavailableWorkspaceStore{Store: workspaceStore}
		handler.workspaceTaskStore = unavailable
		handler.groupRequirements = grouprequirements.NewService(unavailable, grouprequirements.NewMemoryStore())
		_, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
		group := plan.GroupRequirement
		if group == nil || group.State != grouprequirements.StateTargetAmbiguous || group.Home != nil ||
			group.RequiredHomeRoles.Verification != templateGroupRoleVerificationUnavailable {
			t.Fatalf("storage failure did not fail closed: %+v", group)
		}
	})

	t.Run("duplicate exact key is ambiguous", func(t *testing.T) {
		template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
		handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
		defer cleanup()
		installPlanAgentStore(t, handler)
		for _, name := range []string{"Home One", "Home Two"} {
			home := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: name})
			home.Kind = "group"
			home.OwnerUserID = "local"
			home.SetAssistantProgramState(&agentworkspace.AssistantProgramState{
				SchemaVersion: agentworkspace.AssistantProgramStateSchemaVersion,
				Key:           agentworkspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "neutral", ProgramID: "neutral-program"},
				Declaration:   template.AssistantProgram,
			})
			if err := workspaceStore.Save(home); err != nil {
				t.Fatal(err)
			}
		}
		_, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
		group := plan.GroupRequirement
		if group == nil || group.State != grouprequirements.StateTargetAmbiguous || group.Home != nil ||
			group.RequiredHomeRoles.Verification != templateGroupRoleVerificationUnavailable {
			t.Fatalf("ambiguous target leaked guessed state: %+v", group)
		}
	})
}

func TestTemplateGroupRequirementPlan_IsCurrentOwnerAware(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	preparePolicyHome(t, handler)
	handler.currentUserID = func(context.Context) (string, error) { return "another-owner", nil }

	_, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	group := plan.GroupRequirement
	if group == nil || group.State != grouprequirements.StateHomeCreationReviewRequired || group.Home == nil || group.Home.Exists || group.Home.WorkspaceID != "" {
		t.Fatalf("another owner's Home leaked into projection: %+v", group)
	}
}

func TestTemplateGroupRequirementPlan_StandaloneHasNoHomeIdentity(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRecommended)
	template.StandaloneComposition = &projecttemplates.StandaloneComposition{
		SchemaVersion: projecttemplates.StandaloneCompositionSchemaVersion,
		ProjectRoles:  []projecttemplates.StandaloneRole{{RoleID: "project-lead", SystemPrompt: "Coordinate this standalone project."}},
	}
	handler, _, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)

	code, plan, _ := postTemplateGroupPlan(t, handler, template.ID, "standalone")
	group := plan.GroupRequirement
	if code != http.StatusOK || group == nil || group.State != grouprequirements.StateReadyStandalone ||
		group.SelectedComposition != grouprequirements.CompositionStandalone || group.Home != nil ||
		group.RequiredHomeRoles.Verification != templateGroupRoleVerificationNotApplicable {
		t.Fatalf("standalone projection = status %d, %+v", code, group)
	}
}

func TestTemplateGroupRequirementPlan_SourceRevisionTracksTrustedSourceAndDefinition(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)

	_, first, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	firstRevision := first.GroupRequirement.SourceRevision
	template.Revision = strings.Repeat("b", 64)
	_, sourceChanged, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	if sourceChanged.GroupRequirement.SourceRevision == firstRevision {
		t.Fatal("trusted source revision change did not invalidate the projection")
	}
	template.GroupRequirement.DefaultHomeName = "Changed Proposed Home"
	_, definitionChanged, _ := postTemplateGroupPlan(t, handler, template.ID, "")
	if definitionChanged.GroupRequirement.SourceRevision == sourceChanged.GroupRequirement.SourceRevision {
		t.Fatal("group definition change did not invalidate the projection")
	}
}

func TestTemplateGroupRequirementPlan_InvalidSourceReturnsNoProjection(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	handler.projectTemplateResolver = func(string, string) (projecttemplates.Template, error) {
		return projecttemplates.Template{}, projecttemplates.ErrTemplateNotFound
	}
	code, plan, body := postTemplateGroupPlan(t, handler, "missing", "")
	if code == http.StatusOK || plan.GroupRequirement != nil || len(body) == 0 {
		t.Fatalf("invalid source response = status %d plan=%+v body=%v", code, plan.GroupRequirement, body)
	}
	ids, _ := workspaceStore.List()
	if len(ids) != 0 {
		t.Fatalf("invalid source mutated workspaces: %v", ids)
	}
}

func slicesContainAction(actions []grouprequirements.Action, wanted grouprequirements.Action) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
