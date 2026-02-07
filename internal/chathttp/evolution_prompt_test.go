package chathttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestResolveSystemPromptForAgent_RespectsCustomPrompt(t *testing.T) {
	ag := &agent.Agent{
		Settings: types.Settings{
			SystemPrompt: "custom prompt override",
		},
		Evolution: &types.AgentEvolution{
			Path: types.AgentPathCoder,
		},
	}

	got := resolveSystemPromptForAgent(ag, "default prompt")
	if got != "custom prompt override" {
		t.Fatalf("expected custom prompt override, got %q", got)
	}
}

func TestResolveSystemPromptForAgent_AddsPathDefaults(t *testing.T) {
	ag := &agent.Agent{
		Evolution: &types.AgentEvolution{
			Path: types.AgentPathResearcher,
		},
	}

	got := resolveSystemPromptForAgent(ag, "default prompt")
	if got == "default prompt" {
		t.Fatal("expected researcher path guidance to be appended")
	}
	if got == "" {
		t.Fatal("expected non-empty prompt")
	}
}

func TestPrioritizeToolsForPath_Coder(t *testing.T) {
	ag := &agent.Agent{
		Evolution: &types.AgentEvolution{
			Path: types.AgentPathCoder,
		},
	}
	tools := []llm.Tool{
		{Name: "web_search", Description: "search the web"},
		{Name: "git_commit", Description: "commit repository changes"},
		{Name: "notes_write", Description: "write note"},
	}

	prioritized := prioritizeToolsForPath(ag, tools)
	if len(prioritized) != len(tools) {
		t.Fatalf("expected %d tools, got %d", len(tools), len(prioritized))
	}
	if prioritized[0].Name != "git_commit" {
		t.Fatalf("expected coding-oriented tool first, got %q", prioritized[0].Name)
	}
}
