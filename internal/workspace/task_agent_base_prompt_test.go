package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestResolveTaskAgentBasePrompt(t *testing.T) {
	ws := &Workspace{
		ID:   "w1",
		Name: "Acme Campaign",
		AgentInstances: []AgentInstance{
			{ID: "i1", Name: "Copywriter", EntryPoint: true, Role: "Voice keeper"},
		},
	}
	h := &LLMTaskHandler{workspaceStore: newTestWorkspaceStore(t, ws)}
	task := Task{WorkspaceID: "w1", Description: "Write three launch posts."}

	// Variable-bearing base prompt resolves, reports hadVars=true.
	varsAgent := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{
		SystemPrompt: "You are {{agent.role}} for {{workspace.name}}. Goal: {{task.goal}}",
	}}}
	resolved, hadVars := h.resolveTaskAgentBasePrompt(context.Background(), varsAgent, "Copywriter", task)
	if !hadVars {
		t.Fatal("expected hadVars=true")
	}
	for _, want := range []string{"Voice keeper", "Acme Campaign", "Write three launch posts."} {
		if !strings.Contains(resolved, want) {
			t.Errorf("resolved task base prompt missing %q in:\n%s", want, resolved)
		}
	}
	if strings.Contains(resolved, "{{") {
		t.Errorf("no token should survive: %s", resolved)
	}

	// Plain base prompt: hadVars=false, so task behavior is left unchanged.
	plainAgent := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Plain persona."}}}
	if _, hadVars := h.resolveTaskAgentBasePrompt(context.Background(), plainAgent, "Copywriter", task); hadVars {
		t.Error("expected hadVars=false for plain prompt (no task-behavior change)")
	}
}
