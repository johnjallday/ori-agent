package sessionhttp

import (
	"errors"
	"slices"
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
	return slices.Contains(ws.Agents, name)
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
}

func TestSeedTemplateAgents_EmptyRosterNoop(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	ws := &session.Workspace{ID: "ws1", Name: "Plain"}
	res := handler.seedTemplateAgents(ws, projecttemplates.Template{})

	if res.EntrySet || len(res.Warnings) != 0 {
		t.Fatalf("expected empty no-op, got EntrySet=%v warnings=%v", res.EntrySet, res.Warnings)
	}
	if len(ws.Agents) != 0 || currentWorkspaceEntryAgentName(ws) != "" {
		t.Fatalf("expected no agents seeded, got %v / %q", ws.Agents, currentWorkspaceEntryAgentName(ws))
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
	if len(ws.Agents) != 0 || currentWorkspaceEntryAgentName(ws) != "" {
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
