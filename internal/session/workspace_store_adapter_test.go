package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceStoreAdapter_MCPRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:          "workspace-1",
		Name:        "Workspace",
		Description: "Test",
		CreatedAt:   now,
		UpdatedAt:   now,
		MCPBindings: []workspace.WorkspaceMCPBinding{
			{
				ID:         "binding-1",
				ServerName: "filesystem",
				Alias:      "repo_fs",
				Enabled:    true,
				Scope: map[string]interface{}{
					"roots": []string{"/tmp/repo"},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		AgentMCPAccess: []workspace.WorkspaceAgentMCPAccess{
			{
				AgentInstanceID:   "agent-1",
				EnabledBindingIDs: []string{"binding-1"},
				UpdatedAt:         now,
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if len(sessionWS.MCPBindingsJSON) == 0 {
		t.Fatalf("expected MCP bindings JSON to be serialized")
	}
	if len(sessionWS.AgentMCPAccessJSON) == 0 {
		t.Fatalf("expected agent MCP access JSON to be serialized")
	}

	var rawBindings []map[string]interface{}
	if err := json.Unmarshal(sessionWS.MCPBindingsJSON, &rawBindings); err != nil {
		t.Fatalf("failed to decode serialized MCP bindings: %v", err)
	}
	if len(rawBindings) != 1 {
		t.Fatalf("expected 1 serialized MCP binding, got %d", len(rawBindings))
	}

	roundTripped := adapter.toAgentStudioWorkspace(sessionWS)
	if len(roundTripped.MCPBindings) != 1 {
		t.Fatalf("expected 1 round-tripped MCP binding, got %d", len(roundTripped.MCPBindings))
	}
	if roundTripped.MCPBindings[0].ServerName != "filesystem" {
		t.Fatalf("expected round-tripped server_name filesystem, got %q", roundTripped.MCPBindings[0].ServerName)
	}
	if len(roundTripped.AgentMCPAccess) != 1 {
		t.Fatalf("expected 1 round-tripped MCP access rule, got %d", len(roundTripped.AgentMCPAccess))
	}
	if roundTripped.AgentMCPAccess[0].AgentInstanceID != "agent-1" {
		t.Fatalf("expected round-tripped agent_instance_id agent-1, got %q", roundTripped.AgentMCPAccess[0].AgentInstanceID)
	}
}
