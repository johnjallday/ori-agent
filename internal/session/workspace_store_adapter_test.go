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
				Scope: map[string]any{
					"roots": []string{"/tmp/repo"},
				},
				Config: map[string]any{
					"env": map[string]any{
						"ORI_SCOPE": "workspace",
					},
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

	var rawBindings []map[string]any
	if err := json.Unmarshal(sessionWS.MCPBindingsJSON, &rawBindings); err != nil {
		t.Fatalf("failed to decode serialized MCP bindings: %v", err)
	}
	if len(rawBindings) != 1 {
		t.Fatalf("expected 1 serialized MCP binding, got %d", len(rawBindings))
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if len(roundTripped.MCPBindings) != 1 {
		t.Fatalf("expected 1 round-tripped MCP binding, got %d", len(roundTripped.MCPBindings))
	}
	if roundTripped.MCPBindings[0].ServerName != "filesystem" {
		t.Fatalf("expected round-tripped server_name filesystem, got %q", roundTripped.MCPBindings[0].ServerName)
	}
	if roundTripped.MCPBindings[0].Config == nil {
		t.Fatalf("expected round-tripped config to be preserved")
	}
	if len(roundTripped.AgentMCPAccess) != 1 {
		t.Fatalf("expected 1 round-tripped MCP access rule, got %d", len(roundTripped.AgentMCPAccess))
	}
	if roundTripped.AgentMCPAccess[0].AgentInstanceID != "agent-1" {
		t.Fatalf("expected round-tripped agent_instance_id agent-1, got %q", roundTripped.AgentMCPAccess[0].AgentInstanceID)
	}
}

func TestWorkspaceStoreAdapter_OwnerUserIDRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:          "workspace-owner",
		Name:        "Owner Workspace",
		OwnerUserID: "user-1",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	sessionWS := adapter.toSessionWorkspace(input)
	if sessionWS.OwnerUserID != "user-1" {
		t.Fatalf("expected session owner user-1, got %q", sessionWS.OwnerUserID)
	}
	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if roundTripped.OwnerUserID != "user-1" {
		t.Fatalf("expected round-tripped owner user-1, got %q", roundTripped.OwnerUserID)
	}
}

func TestWorkspaceStoreAdapter_AgentInstanceMetadataRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:        "workspace-roles",
		Name:      "Workspace Roles",
		CreatedAt: now,
		UpdatedAt: now,
		AgentInstances: []workspace.AgentInstance{
			{
				ID:             "agent-manager",
				Name:           "Trip Planning Manager",
				InstanceNumber: 1,
				NodeID:         "trip-manager-node",
				Role:           "Manager",
				Description:    "Default entry point for workspace requests.",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	roundTripped := adapter.toAgentWorkspace(sessionWS)

	if len(roundTripped.AgentInstances) != 1 {
		t.Fatalf("expected 1 round-tripped agent instance, got %d", len(roundTripped.AgentInstances))
	}

	got := roundTripped.AgentInstances[0]
	if got.Role != "Manager" {
		t.Fatalf("expected role to round-trip, got %q", got.Role)
	}
	if got.Description != "Default entry point for workspace requests." {
		t.Fatalf("expected description to round-trip, got %q", got.Description)
	}
	if !got.EntryPoint {
		t.Fatal("expected entry_point to round-trip as true")
	}
}

func TestWorkspaceStoreAdapter_SkillRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:        "workspace-skills",
		Name:      "Workspace Skills",
		CreatedAt: now,
		UpdatedAt: now,
		SkillBindings: []workspace.WorkspaceSkillBinding{
			{
				ID:        "binding-1",
				SkillName: "workspace-planning",
				Enabled:   true,
				Trusted:   true,
				Config: map[string]any{
					"profile_type":           "workspace_planning",
					"default_execution_mode": "step_through",
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		AgentSkillAccess: []workspace.WorkspaceAgentSkillAccess{
			{
				AgentInstanceID:   "agent-1",
				EnabledBindingIDs: []string{"binding-1"},
				UpdatedAt:         now,
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if len(sessionWS.SkillBindingsJSON) == 0 {
		t.Fatalf("expected skill bindings JSON to be serialized")
	}
	if len(sessionWS.AgentSkillAccessJSON) == 0 {
		t.Fatalf("expected agent skill access JSON to be serialized")
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if len(roundTripped.SkillBindings) != 1 {
		t.Fatalf("expected 1 round-tripped skill binding, got %d", len(roundTripped.SkillBindings))
	}
	if roundTripped.SkillBindings[0].SkillName != "workspace-planning" {
		t.Fatalf("expected round-tripped skill_name workspace-planning, got %q", roundTripped.SkillBindings[0].SkillName)
	}
	if roundTripped.SkillBindings[0].Config == nil {
		t.Fatalf("expected round-tripped skill config to be preserved")
	}
	if len(roundTripped.AgentSkillAccess) != 1 {
		t.Fatalf("expected 1 round-tripped skill access rule, got %d", len(roundTripped.AgentSkillAccess))
	}
	if roundTripped.AgentSkillAccess[0].AgentInstanceID != "agent-1" {
		t.Fatalf("expected round-tripped agent_instance_id agent-1, got %q", roundTripped.AgentSkillAccess[0].AgentInstanceID)
	}
}

func TestWorkspaceStoreAdapter_WorkspaceFoldersRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:        "workspace-folders",
		Name:      "Workspace Folders",
		CreatedAt: now,
		UpdatedAt: now,
		Folders: []workspace.WorkspaceFolder{
			{
				ID:        "folder-1",
				Path:      "research/notes",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Layout: &workspace.CanvasLayout{
			DirectoryPositions: map[string]workspace.Position{
				"dir-1": {X: 100, Y: 120},
			},
			FolderPositions: map[string]workspace.Position{
				"folder-1": {X: 320, Y: 240},
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if len(sessionWS.FoldersJSON) == 0 {
		t.Fatalf("expected folders JSON to be serialized")
	}
	if sessionWS.Layout == nil {
		t.Fatalf("expected layout to be converted")
	}
	if got := sessionWS.Layout.FolderPositions["folder-1"]; got.X != 320 || got.Y != 240 {
		t.Fatalf("expected folder position to convert to session layout, got %#v", got)
	}
	if got := sessionWS.Layout.DirectoryPositions["dir-1"]; got.X != 100 || got.Y != 120 {
		t.Fatalf("expected directory position to convert to session layout, got %#v", got)
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if len(roundTripped.Folders) != 1 {
		t.Fatalf("expected 1 round-tripped folder, got %d", len(roundTripped.Folders))
	}
	if roundTripped.Folders[0].Path != "research/notes" {
		t.Fatalf("expected folder path research/notes, got %q", roundTripped.Folders[0].Path)
	}
	if roundTripped.Layout == nil {
		t.Fatalf("expected round-tripped layout")
	}
	if got := roundTripped.Layout.FolderPositions["folder-1"]; got.X != 320 || got.Y != 240 {
		t.Fatalf("expected folder position to round-trip, got %#v", got)
	}
	if got := roundTripped.Layout.DirectoryPositions["dir-1"]; got.X != 100 || got.Y != 120 {
		t.Fatalf("expected directory position to round-trip, got %#v", got)
	}
}
