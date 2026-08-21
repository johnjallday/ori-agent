package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestWorkspaceStoreAdapter_AgentInstanceCustomInstructionsRoundTrip verifies the
// per-instance custom_instructions field survives both adapter directions and the
// SQLite JSON (de)serialization used for AgentInstances (PRD FR17).
func TestWorkspaceStoreAdapter_AgentInstanceCustomInstructionsRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	const custom = "First line of guidance.\nSecond line — keep internal newlines."
	input := &workspace.Workspace{
		ID:        "ws-ci",
		Name:      "CI Workspace",
		CreatedAt: now,
		UpdatedAt: now,
		AgentInstances: []workspace.AgentInstance{
			{
				ID:                 "inst-1",
				Name:               "Brand Copywriter",
				InstanceNumber:     1,
				NodeID:             "brand-node-1",
				Role:               "Voice keeper",
				Description:        "Owns tone",
				CustomInstructions: custom,
				EntryPoint:         true,
				CreatedAt:          now,
			},
		},
	}

	// workspace -> session -> workspace preserves the field.
	sessionWS := adapter.toSessionWorkspace(input)
	if got := sessionWS.AgentInstances[0].CustomInstructions; got != custom {
		t.Fatalf("session AgentInstance custom_instructions = %q, want %q", got, custom)
	}
	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if got := roundTripped.AgentInstances[0].CustomInstructions; got != custom {
		t.Fatalf("round-tripped custom_instructions = %q, want %q", got, custom)
	}

	// The SQLite store serializes AgentInstances as a JSON blob; confirm the
	// field marshals/unmarshals intact (internal newlines preserved).
	data, err := json.Marshal(sessionWS.AgentInstances)
	if err != nil {
		t.Fatalf("marshal AgentInstances: %v", err)
	}
	var decoded []AgentInstance
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal AgentInstances: %v", err)
	}
	if got := decoded[0].CustomInstructions; got != custom {
		t.Fatalf("JSON round-trip custom_instructions = %q, want %q", got, custom)
	}
}

func TestWorkspaceStoreAdapter_MCPRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:          "workspace-1",
		Name:        "Workspace",
		Description: "Test",
		CreatedAt:   now,
		UpdatedAt:   now,
		MCPBindings: []workspace.MCPBinding{
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
		AgentMCPAccess: []workspace.AgentMCPAccess{
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

func TestWorkspaceStoreAdapter_FolderSlugRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	input := &workspace.Workspace{
		ID:         "workspace-slug",
		Name:       "Marketing Site",
		FolderSlug: "marketing-site",
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if sessionWS.FolderSlug != "marketing-site" {
		t.Fatalf("session folder slug = %q, want marketing-site", sessionWS.FolderSlug)
	}
	if roundTripped := adapter.toAgentWorkspace(sessionWS); roundTripped.FolderSlug != "marketing-site" {
		t.Fatalf("round-tripped folder slug = %q, want marketing-site", roundTripped.FolderSlug)
	}
}

func TestWorkspaceStoreAdapter_TicketMigrationStateRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	input := &workspace.Workspace{
		ID:                     "workspace-ticket-migration",
		Name:                   "Ticket Migration",
		TicketMigrationVersion: workspace.TicketMigrationVersion,
		TicketSequence:         42,
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if sessionWS.TicketMigrationVersion != workspace.TicketMigrationVersion {
		t.Fatalf("session ticket migration version = %d, want %d", sessionWS.TicketMigrationVersion, workspace.TicketMigrationVersion)
	}
	if sessionWS.TicketSequence != 42 {
		t.Fatalf("session ticket sequence = %d, want 42", sessionWS.TicketSequence)
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if roundTripped.TicketMigrationVersion != workspace.TicketMigrationVersion {
		t.Fatalf("round-tripped ticket migration version = %d, want %d", roundTripped.TicketMigrationVersion, workspace.TicketMigrationVersion)
	}
	if roundTripped.TicketSequence != 42 {
		t.Fatalf("round-tripped ticket sequence = %d, want 42", roundTripped.TicketSequence)
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
		SkillBindings: []workspace.SkillBinding{
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
		AgentSkillAccess: []workspace.AgentSkillAccess{
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
		Folders: []workspace.Folder{
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

// TestWorkspaceStoreAdapter_StationPositionsRoundTrip guards the HQ
// station-layout conversion path: StationPositions uses fractional [0,1]
// coordinates (unlike every other position map on CanvasLayout, which are
// pixel-based), so a naive copy-paste of the pixel conversion would still
// compile but silently drop or mis-scale the values. This proves the field
// survives workspace->session->workspace unchanged.
func TestWorkspaceStoreAdapter_StationPositionsRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:        "workspace-stations",
		Name:      "Workspace Stations",
		CreatedAt: now,
		UpdatedAt: now,
		Layout: &workspace.CanvasLayout{
			StationPositions: map[string]workspace.Position{
				"email": {X: 0.92, Y: 0.15},
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if sessionWS.Layout == nil {
		t.Fatalf("expected layout to be converted")
	}
	if got := sessionWS.Layout.StationPositions["email"]; got.X != 0.92 || got.Y != 0.15 {
		t.Fatalf("expected station position to convert to session layout, got %#v", got)
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if roundTripped.Layout == nil {
		t.Fatalf("expected round-tripped layout")
	}
	if got := roundTripped.Layout.StationPositions["email"]; got.X != 0.92 || got.Y != 0.15 {
		t.Fatalf("expected station position to round-trip, got %#v", got)
	}
}

// A binding's runtime kind must survive session persistence: a mailbox binding
// that lost its `native_email` marker on save would be re-classified as an MCP
// server on the next load, which is the failure this field exists to prevent.
// Legacy records with no field must keep decoding unchanged (FR 23; Rollout 1).
func TestWorkspaceStoreAdapter_RuntimeKindRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	input := &workspace.Workspace{
		ID:        "workspace-1",
		Name:      "Email Ops",
		CreatedAt: now,
		UpdatedAt: now,
		MCPBindings: []workspace.MCPBinding{
			{
				ID:          "binding-mail",
				ServerName:  "gmail",
				Enabled:     true,
				RuntimeKind: workspace.RuntimeKindNativeEmail,
				Config:      map[string]any{"account_id": "acct-1"},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			{
				ID:         "binding-fs",
				ServerName: "filesystem",
				Enabled:    true,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
		},
	}

	sessionWS := adapter.toSessionWorkspace(input)
	if !strings.Contains(string(sessionWS.MCPBindingsJSON), `"runtime_kind":"native_email"`) {
		t.Fatalf("serialized bindings lost runtime_kind: %s", sessionWS.MCPBindingsJSON)
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if len(roundTripped.MCPBindings) != 2 {
		t.Fatalf("expected 2 round-tripped bindings, got %d", len(roundTripped.MCPBindings))
	}
	mail := roundTripped.MCPBindings[0]
	if mail.RuntimeKind != workspace.RuntimeKindNativeEmail || !mail.IsNativeEmail() {
		t.Fatalf("mail binding = %+v, want native_email", mail)
	}
	if fs := roundTripped.MCPBindings[1]; fs.RuntimeKind != "" || !fs.IsRuntimeMCP() {
		t.Fatalf("filesystem binding = %+v, want an unset kind classified as mcp", fs)
	}
}

// Records written before the field existed decode without it and still classify
// correctly from their server name.
func TestWorkspaceStoreAdapter_LegacyBindingWithoutRuntimeKind(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	sessionWS := &Workspace{
		ID:              "workspace-1",
		Name:            "Email Ops",
		MCPBindingsJSON: json.RawMessage(`[{"id":"legacy","server_name":"gmail","enabled":true}]`),
	}

	roundTripped := adapter.toAgentWorkspace(sessionWS)
	if len(roundTripped.MCPBindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(roundTripped.MCPBindings))
	}
	binding := roundTripped.MCPBindings[0]
	if binding.RuntimeKind != "" {
		t.Fatalf("legacy record gained runtime_kind %q", binding.RuntimeKind)
	}
	if !binding.IsNativeEmail() {
		t.Fatal("a legacy gmail binding must still classify as native email")
	}
}
