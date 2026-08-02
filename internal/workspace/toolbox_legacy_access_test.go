package workspace

import (
	"testing"
)

// Coverage for the legacy access-endpoint bridge (task 1.12; PRD FR-36).

func newLegacyBridgeWorkspace(t *testing.T) (*Workspace, *ToolboxDefinition) {
	t.Helper()
	ws := &Workspace{
		ID: "ws-legacy-bridge",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
	}

	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID:   "tbx-1",
		Name: "Workspace Default",
		Skills: []ToolboxSkillRef{
			{CapabilityID: "code-review", DisplayName: "code-review", Source: ToolboxSourceAgentLearned},
			{CapabilityID: "testing", DisplayName: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
		},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}},
	})
	if err != nil {
		t.Fatalf("CreateToolbox() error = %v", err)
	}
	return ws, created
}

// A legacy skill-access write becomes a new Toolbox version, so the endpoint
// keeps having an effect instead of silently writing a field nothing reads.
func TestApplyLegacyAccessToToolbox_SkillAccessProducesANewVersion(t *testing.T) {
	ws, created := newLegacyBridgeWorkspace(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	assignment, bridged, err := ApplyLegacyAccessToToolbox(ws, "inst-1", LegacyAccessSkills, []string{"sb-2"}, "test")
	if err != nil || !bridged {
		t.Fatalf("ApplyLegacyAccessToToolbox() bridged=%v err=%v", bridged, err)
	}
	if assignment.ToolboxVersion != 2 {
		t.Fatalf("expected the bridge to produce version 2, got %d", assignment.ToolboxVersion)
	}

	_, recipe, ok, err := ws.ResolveAssignedToolbox("inst-1")
	if err != nil || !ok {
		t.Fatalf("ResolveAssignedToolbox() ok=%v err=%v", ok, err)
	}
	assertStringsEqual(t, "rewritten skills", skillIdentities(recipe.Skills), []string{
		"code-review/" + ToolboxSourceAgentLearned,
		"drafting/" + ToolboxSourceWorkspaceProvided,
	})
	if len(recipe.MCPBindings) != 1 {
		t.Fatalf("expected the MCP half to be left alone by a skill-access write, got %+v", recipe.MCPBindings)
	}
}

// An MCP-access write rewrites only the MCP half, and an all-tools binding
// becomes an inherited entry rather than an invented subset.
func TestApplyLegacyAccessToToolbox_MCPAccessRewritesOnlyBindings(t *testing.T) {
	ws, created := newLegacyBridgeWorkspace(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	if _, _, err := ApplyLegacyAccessToToolbox(ws, "inst-1", LegacyAccessMCP, []string{"mb-2"}, "test"); err != nil {
		t.Fatalf("ApplyLegacyAccessToToolbox() error = %v", err)
	}

	_, recipe, _, err := ws.ResolveAssignedToolbox("inst-1")
	if err != nil {
		t.Fatalf("ResolveAssignedToolbox() error = %v", err)
	}
	assertStringsEqual(t, "rewritten bindings", bindingIDs(recipe.MCPBindings), []string{"mb-2"})
	if !recipe.MCPBindings[0].InheritsBindingTools {
		t.Fatalf("expected an all-tools binding to defer rather than gain an invented subset")
	}
	if len(recipe.Skills) != 2 {
		t.Fatalf("expected the skill half to be left alone by an MCP-access write, got %+v", recipe.Skills)
	}
}

// A Toolbox shared by two instances is FORKED rather than edited in place: a
// legacy write on behalf of one instance must never reach the other (FR-17).
func TestApplyLegacyAccessToToolbox_ForksASharedToolbox(t *testing.T) {
	ws, created := newLegacyBridgeWorkspace(t)
	for _, instanceID := range []string{"inst-1", "inst-2"} {
		if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: instanceID, ToolboxID: created.ID}); err != nil {
			t.Fatalf("SetToolboxAssignment(%s) error = %v", instanceID, err)
		}
	}

	assignment, bridged, err := ApplyLegacyAccessToToolbox(ws, "inst-1", LegacyAccessMCP, []string{"mb-2"}, "test")
	if err != nil || !bridged {
		t.Fatalf("ApplyLegacyAccessToToolbox() bridged=%v err=%v", bridged, err)
	}
	if assignment.ToolboxID == created.ID {
		t.Fatalf("expected a shared toolbox to be forked, but the shared one was edited in place")
	}

	_, untouched, _, err := ws.ResolveAssignedToolbox("inst-2")
	if err != nil {
		t.Fatalf("ResolveAssignedToolbox(inst-2) error = %v", err)
	}
	assertStringsEqual(t, "unshared instance bindings", bindingIDs(untouched.MCPBindings), []string{"mb-1"})
	if untouched.Version != 1 {
		t.Fatalf("expected the other instance to stay pinned to version 1, got %d", untouched.Version)
	}
}

// An unmigrated instance has no assignment, so the legacy write stands alone —
// which is correct, because that instance still resolves through the legacy
// path.
func TestApplyLegacyAccessToToolbox_NoOpWithoutAnAssignment(t *testing.T) {
	ws, _ := newLegacyBridgeWorkspace(t)

	assignment, bridged, err := ApplyLegacyAccessToToolbox(ws, "inst-1", LegacyAccessMCP, []string{"mb-2"}, "test")
	if err != nil {
		t.Fatalf("ApplyLegacyAccessToToolbox() error = %v", err)
	}
	if bridged || assignment != nil {
		t.Fatalf("expected no bridging for an unassigned instance, got bridged=%v", bridged)
	}
}
