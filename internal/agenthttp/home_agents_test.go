package agenthttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// makeTestWorkspaceWithAgents saves a workspace with the given agent members so
// agent→workspace cross-referencing can be exercised.
func makeTestWorkspaceWithAgents(t *testing.T, store workspace.Store, id, name string, agents []string) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name, Agents: agents})
	ws.ID = id
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

func agentRoster() stubAgentsReader {
	return stubAgentsReader{ok: true, roster: []HomeAgentSummary{
		{Name: "Ori", Type: "tool-calling", Role: "orchestrator", Model: "gpt-5", Provider: "openai", Capabilities: []string{"planning"}},
		{Name: "Scout", Type: "research", Role: "researcher", Model: "claude-sonnet-4-6", Provider: "anthropic"},
		{Name: "Idle", Type: "general", Role: "assistant", Model: "gpt-5-mini", Provider: "openai"},
	}}
}

func TestBuildHomeSnapshot_AgentWorkspaceCrossReference(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspaceWithAgents(t, store, "ws-1", "Alpha", []string{"Ori", "Scout"})
	makeTestWorkspaceWithAgents(t, store, "ws-2", "Beta", []string{"Ori"})

	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces: store,
		Agents:     agentRoster(),
		Now:        fixedNow,
	}, HomeWindowThisWeek)

	if snap.Meta.AgentCount != 3 {
		t.Fatalf("AgentCount = %d, want 3", snap.Meta.AgentCount)
	}
	byName := map[string]HomeAgentSummary{}
	for _, a := range snap.Agents {
		byName[a.Name] = a
	}
	// Ori is used in both workspaces and should sort first (most-used).
	if snap.Agents[0].Name != "Ori" {
		t.Errorf("expected Ori first (most used), got %q", snap.Agents[0].Name)
	}
	if got := byName["Ori"]; got.WorkspaceCount != 2 || len(got.Workspaces) != 2 {
		t.Errorf("Ori workspace usage = %d %v, want 2", got.WorkspaceCount, got.Workspaces)
	}
	if got := byName["Scout"]; got.WorkspaceCount != 1 || got.Workspaces[0] != "Alpha" {
		t.Errorf("Scout workspace usage = %d %v, want 1 [Alpha]", got.WorkspaceCount, got.Workspaces)
	}
	if got := byName["Idle"]; got.WorkspaceCount != 0 || len(got.Workspaces) != 0 {
		t.Errorf("Idle should be used nowhere, got %d %v", got.WorkspaceCount, got.Workspaces)
	}

	if txt := snap.PromptText(); !strings.Contains(txt, "### Agents (3)") || !strings.Contains(txt, "\"Ori\"") {
		t.Errorf("prompt text missing agents section: %q", txt)
	}
}

func TestBuildHomeSnapshot_AgentsDegradedWhenNoReader(t *testing.T) {
	store := workspace.NewInMemoryStore()
	snap := BuildHomeSnapshot(context.Background(), HomeSnapshotSources{
		Workspaces: store,
		Now:        fixedNow,
	}, HomeWindowThisWeek)
	if !containsString(snap.Meta.Degraded, "agents") {
		t.Errorf("expected agents degraded when no reader, got %v", snap.Meta.Degraded)
	}
}

func TestHomeAgentsTool_FiltersAndUsage(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspaceWithAgents(t, store, "ws-1", "Alpha", []string{"Ori", "Scout"})
	makeTestWorkspaceWithAgents(t, store, "ws-2", "Beta", []string{"Ori"})

	reg := newHomeToolRegistry(HomeSnapshotSources{
		Workspaces: store,
		Agents:     agentRoster(),
		Now:        fixedNow,
	})

	if !reg.Has("home_agents") {
		t.Fatal("home_agents not registered")
	}

	// No filter: all three agents, Ori first with usage 2.
	out, err := reg.Execute(context.Background(), "home_agents", "{}")
	if err != nil {
		t.Fatalf("execute home_agents: %v", err)
	}
	var all struct {
		Agents []struct {
			Name           string   `json:"name"`
			WorkspaceCount int      `json:"workspace_count"`
			Workspaces     []string `json:"workspaces"`
		} `json:"agents"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out)
	}
	if all.Total != 3 || len(all.Agents) != 3 {
		t.Fatalf("expected 3 agents, got total=%d len=%d", all.Total, len(all.Agents))
	}
	if all.Agents[0].Name != "Ori" || all.Agents[0].WorkspaceCount != 2 {
		t.Errorf("expected Ori first with usage 2, got %+v", all.Agents[0])
	}

	// Name filter.
	out, _ = reg.Execute(context.Background(), "home_agents", `{"name":"sco"}`)
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatalf("unmarshal name filter: %v", err)
	}
	if all.Total != 1 || all.Agents[0].Name != "Scout" {
		t.Errorf("name filter 'sco' should match only Scout, got %+v", all.Agents)
	}

	// workspace_id filter: only agents used by ws-2 (Ori).
	out, _ = reg.Execute(context.Background(), "home_agents", `{"workspace_id":"ws-2"}`)
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatalf("unmarshal ws filter: %v", err)
	}
	if all.Total != 1 || all.Agents[0].Name != "Ori" {
		t.Errorf("workspace_id filter ws-2 should match only Ori, got %+v", all.Agents)
	}
}

func TestHomeAgentsTool_UnavailableReader(t *testing.T) {
	reg := newHomeToolRegistry(HomeSnapshotSources{Now: fixedNow})
	out, err := reg.Execute(context.Background(), "home_agents", "{}")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Errorf("expected unavailable note, got %s", out)
	}
}

func newAgentAssignHandler(t *testing.T) *HomeAssistantAskHandler {
	t.Helper()
	store := workspace.NewInMemoryStore()
	makeTestWorkspaceWithAgents(t, store, "ws-1", "Alpha", []string{"Ori"})
	return NewHomeAssistantAskHandler(
		HomeSnapshotSources{Workspaces: store, Agents: agentRoster(), Now: fixedNow},
		nil,
		nil,
	)
}

func TestDetectMutation_AssignAgent(t *testing.T) {
	h := newAgentAssignHandler(t)

	conf := h.detectHomeMutationRequest("add agent Scout to Alpha")
	if conf == nil {
		t.Fatal("expected an assign_agent confirmation")
	}
	if conf.ActionType != HomeActionAssignAgent {
		t.Errorf("action type = %q, want %q", conf.ActionType, HomeActionAssignAgent)
	}
	if got, _ := conf.Arguments["workspace_id"].(string); got != "ws-1" {
		t.Errorf("workspace_id = %q, want ws-1", got)
	}
	if got, _ := conf.Arguments["agent_name"].(string); got != "Scout" {
		t.Errorf("agent_name = %q, want Scout", got)
	}
}

func TestDetectMutation_AssignAgentUnknownAgentDeclines(t *testing.T) {
	h := newAgentAssignHandler(t)
	if conf := h.detectHomeMutationRequest("add agent Nonexistent to Alpha"); conf != nil {
		t.Fatalf("expected no confirmation for unknown agent, got %+v", conf)
	}
}

func TestDetectMutation_AssignAgentUnknownWorkspaceDeclines(t *testing.T) {
	h := newAgentAssignHandler(t)
	if conf := h.detectHomeMutationRequest("add agent Scout to Nowhere"); conf != nil {
		t.Fatalf("expected no confirmation for unknown workspace, got %+v", conf)
	}
}

func TestDetectMutation_AddTaskNotMisreadAsAgent(t *testing.T) {
	h := newAgentAssignHandler(t)
	// "add a task ... in <workspace>" must remain a create_task, not assign_agent.
	conf := h.detectHomeMutationRequest("add a task to review metrics in Alpha")
	if conf == nil {
		t.Fatal("expected a create_task confirmation")
	}
	if conf.ActionType != HomeActionCreateTask {
		t.Errorf("action type = %q, want %q", conf.ActionType, HomeActionCreateTask)
	}
}

func TestAsk_ConfirmAndExecuteAssignAgent(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspaceWithAgents(t, store, "ws-1", "Alpha", []string{"Ori"})
	factory := llm.NewFactory()
	factory.Register("fake", &fakeProvider{content: "irrelevant"})
	h := NewHomeAssistantAskHandler(
		HomeSnapshotSources{Workspaces: store, Agents: agentRoster(), Now: fixedNow},
		factory,
		stubSystemModel{provider: "fake", model: "fake-model"},
	)
	mut := &recordingMutator{}
	h.SetMutator(mut)

	resp := h.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: "assign Scout to Alpha",
		Intent: "app_introspection",
	})
	if !resp.RequiresConfirmation || resp.Confirmation == nil {
		t.Fatalf("expected an assign_agent confirmation, got %+v", resp)
	}
	if resp.Confirmation.ActionType != HomeActionAssignAgent {
		t.Fatalf("confirmation type = %q, want %q", resp.Confirmation.ActionType, HomeActionAssignAgent)
	}

	resp2 := h.Ask(context.Background(), HomeAssistantAskRequest{
		Intent: "app_introspection",
		ConfirmedAction: &HomeAction{
			Type:      HomeActionAssignAgent,
			Arguments: resp.Confirmation.Arguments,
		},
	})
	if mut.assignedWS != "ws-1" || mut.assignedAgent != "Scout" {
		t.Errorf("AssignAgent called with (%q, %q), want (ws-1, Scout)", mut.assignedWS, mut.assignedAgent)
	}
	foundOpen := false
	for _, a := range resp2.Actions {
		if a.Type == HomeActionOpenWorkspace && a.WorkspaceID == "ws-1" {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Errorf("expected open_workspace action after assign, got %+v", resp2.Actions)
	}
}
