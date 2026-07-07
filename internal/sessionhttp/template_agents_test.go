package sessionhttp

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
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
