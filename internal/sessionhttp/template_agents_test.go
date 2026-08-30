package sessionhttp

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func rosterTemplate(specs ...projecttemplates.AgentSpec) projecttemplates.Template {
	return projecttemplates.Template{Agents: specs}
}

func wsHasAgent(ws *session.Workspace, name string) bool {
	for _, inst := range ws.AgentInstances {
		if inst.Name == name {
			return true
		}
	}
	return false
}

type fakeSystemModelReader struct {
	provider        string
	model           string
	reasoningEffort string
}

func (f fakeSystemModelReader) GetSystemModel() (string, string) {
	return f.provider, f.model
}

func (f fakeSystemModelReader) GetSystemReasoningEffort() string {
	return f.reasoningEffort
}

func TestSeedTemplateAgents_CreatesRosterAndSetsEntry(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead", Role: "orchestrator", SystemPrompt: "run it"},
		projecttemplates.AgentSpec{Name: "Writer"},
		projecttemplates.AgentSpec{Name: "Editor"},
	)

	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected EntrySet true")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", res.Warnings)
	}
	for _, name := range []string{"Lead", "Writer", "Editor"} {
		if _, ok := handler.agentStore.GetAgent(name); !ok {
			t.Fatalf("expected agent %q to be created", name)
		}
		if !wsHasAgent(ws, name) {
			t.Fatalf("expected workspace to list agent %q", name)
		}
	}
	if got := currentWorkspaceEntryAgentName(ws); got != "Lead" {
		t.Fatalf("expected entry agent Lead, got %q", got)
	}
}

func TestSeedTemplateAgents_BlankModelInheritsSystemModel(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetSystemModelReader(fakeSystemModelReader{
		provider:        "codex",
		model:           "gpt-5.3-codex",
		reasoningEffort: "high",
	})

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(projecttemplates.AgentSpec{Name: "Lead", Role: "orchestrator"})

	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected EntrySet true")
	}
	created, ok := handler.agentStore.GetAgent("Lead")
	if !ok {
		t.Fatal("expected Lead agent to be created")
	}
	if created.Settings.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want gpt-5.3-codex", created.Settings.Model)
	}
	if created.Settings.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", created.Settings.Provider)
	}
	if created.Settings.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", created.Settings.ReasoningEffort)
	}
}

func TestSeedTemplateAgents_ExplicitModelWinsOverSystemModel(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetSystemModelReader(fakeSystemModelReader{
		provider: "codex",
		model:    "gpt-5.3-codex",
	})

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(projecttemplates.AgentSpec{Name: "Lead", Model: "gpt-5-mini"})

	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected EntrySet true")
	}
	created, ok := handler.agentStore.GetAgent("Lead")
	if !ok {
		t.Fatal("expected Lead agent to be created")
	}
	if created.Settings.Model != "gpt-5-mini" {
		t.Fatalf("model = %q, want explicit gpt-5-mini", created.Settings.Model)
	}
	if created.Settings.Provider != "" {
		t.Fatalf("provider = %q, want empty provider for explicit model-only template", created.Settings.Provider)
	}
}

func TestBuildTemplateAgentPlan_AssistantProgramExposesCreateWizardHirePlan(t *testing.T) {
	handler := &Handler{}
	plan := handler.buildTemplateAgentPlan(projecttemplates.Template{
		ID:     "plugin:neutral:project",
		Agents: []projecttemplates.AgentSpec{{Name: "Legacy Auto Seed"}},
		AssistantProgram: &agentworkspace.AssistantProgramDeclaration{
			SchemaVersion: 1, ID: "project-guide", StationName: "Guide Home", DefaultPrimaryName: "Guide",
			Roles:  []agentworkspace.AssistantProgramRoleSpec{{ID: "guide", Label: "Guide", Primary: true, SystemPrompt: "Coordinate"}},
			Stages: []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		},
	})
	if plan.HasAgents || len(plan.Agents) != 0 || plan.EntryAgentName != "" {
		t.Fatalf("assistant program leaked ordinary template agents: %+v", plan)
	}
	if plan.AssistantProgram == nil || plan.AssistantProgram.ID != "project-guide" || len(plan.AssistantProgram.Roles) != 1 || len(plan.AssistantProgram.Stages) != 1 {
		t.Fatalf("assistant create-wizard plan = %+v", plan.AssistantProgram)
	}
	if plan.AssistantProgram.Roles[0].Label != "Guide" || !plan.AssistantProgram.Roles[0].Primary {
		t.Fatalf("assistant role plan = %+v", plan.AssistantProgram.Roles)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("assistant wizard should not be presented as a warning: %v", plan.Warnings)
	}
}

func TestBuildTemplateAgentPlan_ExposesExistingSharedRosterWithoutOfferingRename(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetWorkspaceTaskStore(session.NewWorkspaceStoreAdapter(handler.store))

	declaration := &agentworkspace.AssistantProgramDeclaration{
		SchemaVersion: 1, ID: "project-guide", StationName: "Guide Home", DefaultPrimaryName: "Guide",
		Roles: []agentworkspace.AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Primary: true, SystemPrompt: "Coordinate"},
			{ID: "reviewer", Label: "Reviewer", SystemPrompt: "Review"},
		},
		Stages: []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
	}
	station := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Guide Home"})
	station.OwnerUserID = "local"
	station.SetAssistantProgramState(&agentworkspace.AssistantProgramState{
		SchemaVersion: 1,
		StateRevision: 3,
		Key: agentworkspace.AssistantProgramKey{
			OwnerUserID: "local", PluginID: "neutral", ProgramID: "project-guide",
		},
		Declaration: declaration,
		Hired:       true,
		PrimaryName: "June",
		Provider:    "ollama",
		Model:       "gemma",
		Roster: []agentworkspace.AssistantRoleBinding{
			{RoleID: "guide", AgentInstanceID: "guide-1", AgentName: "June"},
			{RoleID: "reviewer", AgentInstanceID: "reviewer-1", AgentName: "Reviewer"},
		},
	})
	if err := handler.workspaceTaskStore.Save(station); err != nil {
		t.Fatalf("save station: %v", err)
	}

	plan := handler.buildTemplateAgentPlan(projecttemplates.Template{
		ID:               "plugin:neutral:project",
		PluginOwner:      &agentworkspace.PluginTemplateOwner{PluginID: "neutral"},
		AssistantProgram: declaration,
	})
	program := plan.AssistantProgram
	if program == nil || !program.ExistingHired || program.ExistingProvider != "ollama" || program.ExistingModel != "gemma" {
		t.Fatalf("existing assistant plan = %+v", program)
	}
	if program.Roles[0].AgentName != "June" || program.Roles[1].AgentName != "Reviewer" {
		t.Fatalf("existing role bindings = %+v", program.Roles)
	}
}

func TestBuildTemplateAgentPlan_CreateReuseAndSystemModel(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetSystemModelReader(fakeSystemModelReader{
		provider:        "codex",
		model:           "gpt-5.3-codex",
		reasoningEffort: "high",
	})
	if err := handler.agentStore.CreateAgent("Shared", &agentstore.CreateAgentConfig{
		Model:        "gpt-5-mini",
		LLMProvider:  "openai",
		SystemPrompt: "saved shared prompt",
	}); err != nil {
		t.Fatalf("pre-create agent: %v", err)
	}

	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Shared", Model: "ignored-model"},
		projecttemplates.AgentSpec{Name: "Fresh", Role: "orchestrator", SystemPrompt: "fresh prompt", Tools: projecttemplates.ToolDefaults{Skills: []string{"planning"}}},
	)
	tpl.ID = "launch"
	tpl.Name = "Launch"

	plan := handler.buildTemplateAgentPlan(tpl)

	if !plan.HasAgents || plan.EntryAgentName != "Shared" {
		t.Fatalf("unexpected plan header: %+v", plan)
	}
	if !plan.SystemModelConfigured || plan.SystemProvider != "codex" || plan.SystemModel != "gpt-5.3-codex" {
		t.Fatalf("unexpected system model fields: %+v", plan)
	}
	if len(plan.Agents) != 2 {
		t.Fatalf("expected 2 planned agents, got %d", len(plan.Agents))
	}
	if plan.Agents[0].Action != "reuse" || plan.Agents[0].Model != "gpt-5-mini" || plan.Agents[0].ModelSource != "existing" {
		t.Fatalf("unexpected reused agent plan: %+v", plan.Agents[0])
	}
	if plan.Agents[0].SystemPrompt != "saved shared prompt" {
		t.Fatalf("expected existing prompt to be surfaced, got %q", plan.Agents[0].SystemPrompt)
	}
	if plan.Agents[1].Action != "create" || plan.Agents[1].Model != "gpt-5.3-codex" || plan.Agents[1].Provider != "codex" || plan.Agents[1].ModelSource != "system" {
		t.Fatalf("unexpected created agent plan: %+v", plan.Agents[1])
	}
	if plan.Agents[1].SystemPrompt != "fresh prompt" {
		t.Fatalf("expected template prompt to be surfaced, got %q", plan.Agents[1].SystemPrompt)
	}
	if len(plan.Agents[1].Tools.Skills) != 1 || plan.Agents[1].Tools.Skills[0] != "planning" {
		t.Fatalf("expected planned tools to be preserved, got %+v", plan.Agents[1].Tools)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "Shared") {
		t.Fatalf("expected reuse warning for Shared, got %v", plan.Warnings)
	}
}

func TestApplyTemplateAgentOverrides_UpdatesEditableFieldsAndPreservesTools(t *testing.T) {
	name := "Edited Lead"
	model := "gpt-5.7"
	provider := "codex"
	prompt := "Lead the workspace."
	idx := 0
	tpl := rosterTemplate(projecttemplates.AgentSpec{
		Name:  "Lead",
		Model: "gpt-5-mini",
		Tools: projecttemplates.ToolDefaults{Skills: []string{"planning"}},
	})

	next, err := applyTemplateAgentOverrides(tpl, []templateAgentOverride{{
		Index:        &idx,
		Name:         &name,
		Model:        &model,
		Provider:     &provider,
		SystemPrompt: &prompt,
	}})
	if err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	got := next.Agents[0]
	if got.Name != "Edited Lead" || got.Model != "gpt-5.7" || got.Provider != "codex" || got.SystemPrompt != "Lead the workspace." {
		t.Fatalf("editable fields not applied: %+v", got)
	}
	if len(got.Tools.Skills) != 1 || got.Tools.Skills[0] != "planning" {
		t.Fatalf("tools should be preserved, got %+v", got.Tools)
	}
}

func TestApplyTemplateAgentOverrides_RejectsDuplicateNames(t *testing.T) {
	name := "Writer"
	idx := 0
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead"},
		projecttemplates.AgentSpec{Name: "Writer"},
	)

	if _, err := applyTemplateAgentOverrides(tpl, []templateAgentOverride{{
		Index: &idx,
		Name:  &name,
	}}); err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestSeedTemplateAgents_ReuseOnNameMatchDoesNotMutate(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Shared", &agentstore.CreateAgentConfig{SystemPrompt: "ORIGINAL"}); err != nil {
		t.Fatalf("pre-create agent: %v", err)
	}

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Shared", SystemPrompt: "IGNORED-ON-REUSE", Model: "some-model"},
		projecttemplates.AgentSpec{Name: "Fresh", SystemPrompt: "new"},
	)

	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected EntrySet true")
	}
	reused, ok := handler.agentStore.GetAgent("Shared")
	if !ok {
		t.Fatal("expected reused agent to still exist")
	}
	if reused.Settings.SystemPrompt != "ORIGINAL" {
		t.Fatalf("reused agent was mutated: prompt = %q", reused.Settings.SystemPrompt)
	}
	if got := currentWorkspaceEntryAgentName(ws); got != "Shared" {
		t.Fatalf("expected entry agent Shared, got %q", got)
	}
	if _, ok := handler.agentStore.GetAgent("Fresh"); !ok {
		t.Fatal("expected unmatched specialist to be created")
	}

	// The reused "Shared" agent must produce exactly one visible reuse notice;
	// the freshly-created "Fresh" must not (PRD FR7).
	if len(res.ReuseNotices) != 1 {
		t.Fatalf("expected 1 reuse notice, got %d: %v", len(res.ReuseNotices), res.ReuseNotices)
	}
	if !strings.Contains(res.ReuseNotices[0], "Shared") {
		t.Errorf("expected reuse notice to name 'Shared', got %q", res.ReuseNotices[0])
	}
}

func TestSeedTemplateAgents_EmptyRosterNoop(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	ws := &session.Workspace{ID: "ws1", Name: "Plain"}
	res := handler.seedTemplateAgents(ws, projecttemplates.Template{})

	if res.EntrySet || len(res.Warnings) != 0 {
		t.Fatalf("expected empty no-op, got EntrySet=%v warnings=%v", res.EntrySet, res.Warnings)
	}
	if len(ws.AgentInstances) != 0 || currentWorkspaceEntryAgentName(ws) != "" {
		t.Fatalf("expected no agents seeded, got %v / %q", ws.AgentInstances, currentWorkspaceEntryAgentName(ws))
	}
}

func TestBlankWorkspaceTemplate_SeedsSingleEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	tpl := blankWorkspaceTemplate()
	if !tpl.HasAgents() || len(tpl.Agents) != 1 {
		t.Fatalf("expected exactly one blank roster agent, got %d", len(tpl.Agents))
	}

	ws := &session.Workspace{ID: "ws-blank", Name: "Plain"}
	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected the blank Workspace Manager to be set as entry agent")
	}
	if got := currentWorkspaceEntryAgentName(ws); got != blankWorkspaceEntryAgentName {
		t.Fatalf("expected entry agent %q, got %q", blankWorkspaceEntryAgentName, got)
	}
	created, ok := handler.agentStore.GetAgent(blankWorkspaceEntryAgentName)
	if !ok {
		t.Fatalf("expected agent %q to be created", blankWorkspaceEntryAgentName)
	}
	if strings.TrimSpace(created.Settings.SystemPrompt) == "" {
		t.Fatal("expected the blank entry agent to carry a system prompt")
	}

	// The plan the review panel renders must advertise the single entry agent.
	plan := handler.buildTemplateAgentPlan(blankWorkspaceTemplate())
	if !plan.HasAgents || plan.EntryAgentName != blankWorkspaceEntryAgentName {
		t.Fatalf("expected plan entry %q, got has_agents=%v entry=%q",
			blankWorkspaceEntryAgentName, plan.HasAgents, plan.EntryAgentName)
	}
}

func TestCanonicalAgentTypeAndRole(t *testing.T) {
	if got := canonicalAgentType("General"); got != agent.TypeGeneral {
		t.Fatalf("type General -> %q", got)
	}
	if got := canonicalAgentType("bogus"); got != "" {
		t.Fatalf("invalid type should be empty, got %q", got)
	}
	if got := canonicalAgentRole("Orchestrator"); got != "orchestrator" {
		t.Fatalf("role Orchestrator -> %q", got)
	}
	// cli_agent is a non-goal and must not pass through.
	if got := canonicalAgentRole("cli_agent"); got != "" {
		t.Fatalf("cli_agent should be excluded, got %q", got)
	}
}

func TestSeedTemplateAgents_TracksCreatedAgentsWithTools(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Reused", &agentstore.CreateAgentConfig{}); err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "NewLead", Tools: projecttemplates.ToolDefaults{Skills: []string{"plan"}}},
		projecttemplates.AgentSpec{Name: "Reused", Tools: projecttemplates.ToolDefaults{Skills: []string{"ignored"}}},
		projecttemplates.AgentSpec{Name: "NewWriter"}, // no tools
		projecttemplates.AgentSpec{Name: "NewDesigner", Tools: projecttemplates.ToolDefaults{MCPServers: []string{"drive"}}},
	)

	res := handler.seedTemplateAgents(ws, tpl)

	// Only newly-created agents that declare tools are tracked for binding:
	// the reused agent and the tool-less agent are excluded.
	if len(res.Created) != 2 {
		t.Fatalf("expected 2 tracked agents, got %d (%+v)", len(res.Created), res.Created)
	}
	if res.Created[0].Name != "NewLead" || res.Created[1].Name != "NewDesigner" {
		t.Fatalf("unexpected tracked agents: %+v", res.Created)
	}
	if res.Created[0].Tools.Skills[0] != "plan" {
		t.Fatalf("tools not carried: %+v", res.Created[0].Tools)
	}
}

func TestBindSeededAgentTools(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	var calls []string
	handler.SetAgentToolApplier(func(workspaceID, agentName string, tools projecttemplates.ToolDefaults) ([]string, []string) {
		calls = append(calls, agentName)
		if agentName == "Missing" {
			return nil, []string{"skill:foo"}
		}
		return []string{"skill:ok"}, nil
	})

	created := []createdAgent{
		{Name: "Good", Tools: projecttemplates.ToolDefaults{Skills: []string{"ok"}}},
		{Name: "Missing", Tools: projecttemplates.ToolDefaults{Skills: []string{"foo"}}},
		{Name: "Empty"}, // no tools -> applier not called
	}

	warnings := handler.bindSeededAgentTools("ws1", created)

	if len(calls) != 2 || calls[0] != "Good" || calls[1] != "Missing" {
		t.Fatalf("applier called for %v, want [Good Missing]", calls)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "Missing") || !strings.Contains(warnings[0], "skill:foo") {
		t.Fatalf("warning missing detail: %q", warnings[0])
	}
}

func TestBindSeededAgentTools_NoApplierOrEmpty(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// No applier wired -> no-op.
	if w := handler.bindSeededAgentTools("ws1", []createdAgent{{Name: "A", Tools: projecttemplates.ToolDefaults{Skills: []string{"x"}}}}); w != nil {
		t.Fatalf("expected nil warnings without an applier, got %v", w)
	}
	// Applier wired but no created agents -> no-op.
	handler.SetAgentToolApplier(func(string, string, projecttemplates.ToolDefaults) ([]string, []string) {
		t.Fatal("applier should not be called for an empty roster")
		return nil, nil
	})
	if w := handler.bindSeededAgentTools("ws1", nil); w != nil {
		t.Fatalf("expected nil warnings for empty created list, got %v", w)
	}
}

// stubAgentStore embeds the store interface so only the two methods the seeder
// uses need bodies; existing holds names that GetAgent reports as present, and
// CreateAgent always fails so the seeding failure paths can be exercised.
type stubAgentStore struct {
	agentstore.Store
	existing map[string]*agent.Agent
}

func (s *stubAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	a, ok := s.existing[name]
	return a, ok
}

func (s *stubAgentStore) CreateAgent(string, *agentstore.CreateAgentConfig) error {
	return errors.New("boom")
}

func TestSeedTemplateAgents_EntryFailureFallsBack(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	handler.SetAgentStore(&stubAgentStore{existing: map[string]*agent.Agent{}})

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead"},
		projecttemplates.AgentSpec{Name: "Writer"},
	)

	res := handler.seedTemplateAgents(ws, tpl)

	if res.EntrySet {
		t.Fatal("expected EntrySet false when the entry agent fails")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected one warning, got %v", res.Warnings)
	}
	if len(ws.AgentInstances) != 0 || currentWorkspaceEntryAgentName(ws) != "" {
		t.Fatal("expected workspace left agent-less so the prompt fires")
	}
}

func TestSeedTemplateAgents_SpecialistFailureContinues(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	// Entry "Lead" already exists (reused, no create); specialist "Writer" is
	// unmatched and CreateAgent fails for it.
	handler.SetAgentStore(&stubAgentStore{existing: map[string]*agent.Agent{
		"Lead": {},
	}})

	ws := &session.Workspace{ID: "ws1", Name: "Campaign"}
	tpl := rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead"},
		projecttemplates.AgentSpec{Name: "Writer"},
	)

	res := handler.seedTemplateAgents(ws, tpl)

	if !res.EntrySet {
		t.Fatal("expected entry agent set from the reused agent")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected one specialist warning, got %v", res.Warnings)
	}
	if currentWorkspaceEntryAgentName(ws) != "Lead" {
		t.Fatalf("expected entry Lead, got %q", currentWorkspaceEntryAgentName(ws))
	}
	if wsHasAgent(ws, "Writer") {
		t.Fatal("failed specialist should not be attached")
	}
}
