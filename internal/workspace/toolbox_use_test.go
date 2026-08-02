package workspace

import (
	"errors"
	"testing"
)

// Preview, use, and undo coverage (tasks 3.19; PRD FR-73–FR-91).

func newUseFixture(t *testing.T) (*Workspace, *ToolboxDefinition, *ToolboxDefinition) {
	t.Helper()
	ws := &Workspace{
		ID:      "ws-use",
		Version: 5,
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{
				ID: "mb-1", ServerName: "notes", Alias: "Notes", Enabled: true,
				AllowedTools:      []string{"read_note", "write_note"},
				DefaultSideEffect: SideEffectRead,
				ToolOverrides:     map[string]SideEffect{"write_note": SideEffectWrite},
			},
			{
				ID: "mb-2", ServerName: "docs", Alias: "Docs", Enabled: true,
				AllowedTools:      []string{"read_doc"},
				DefaultSideEffect: SideEffectRead,
			},
		},
	}

	lean, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-lean",
		Name:        "Lean Kit",
		Skills:      []ToolboxSkillRef{{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1", Required: true}},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}, Required: true}},
	})
	if err != nil {
		t.Fatalf("CreateToolbox(lean) error = %v", err)
	}
	wide, err := ws.CreateToolbox(ToolboxDefinition{
		ID:   "tbx-wide",
		Name: "Wide Kit",
		Skills: []ToolboxSkillRef{
			{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1", Required: true},
			{CapabilityID: "drafting", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-2", Required: true},
		},
		MCPBindings: []ToolboxMCPRef{
			{BindingID: "mb-1", AllowedTools: []string{"read_note", "write_note"}, Required: true},
			{BindingID: "mb-2", AllowedTools: []string{"read_doc"}, Required: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateToolbox(wide) error = %v", err)
	}
	return ws, lean, wide
}

func previewFor(t *testing.T, ws *Workspace, instanceID, toolboxID string) ToolboxPreview {
	t.Helper()
	instance := findAgentInstanceByID(ws, instanceID)
	definition, exists := ws.GetToolbox(toolboxID)
	if !exists {
		t.Fatalf("toolbox %s not found", toolboxID)
	}
	preview := PreviewToolbox(ws, instance, *definition, definition.CurrentRecipe(), nil, 4, false, DefaultFocusThresholds())
	preview.ApplyCurrentAssignmentDiff(ws)
	return preview
}

// FR-76: previewing changes nothing at all.
func TestPreviewToolbox_ChangesNothing(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	beforeVersion := ws.Version
	beforeAssignments := len(ws.ToolboxAssignments)
	beforeSkillAccess := len(ws.AgentSkillAccess)
	beforeBindings := ws.GetMCPBindings()

	for range 3 {
		previewFor(t, ws, "inst-1", lean.ID)
	}

	if ws.Version != beforeVersion {
		t.Fatalf("preview bumped the workspace version %d -> %d", beforeVersion, ws.Version)
	}
	if len(ws.ToolboxAssignments) != beforeAssignments || len(ws.AgentSkillAccess) != beforeSkillAccess {
		t.Fatalf("preview wrote assignment or access state")
	}
	for i, binding := range ws.GetMCPBindings() {
		if binding.Enabled != beforeBindings[i].Enabled ||
			binding.DefaultSideEffect != beforeBindings[i].DefaultSideEffect ||
			len(binding.AllowedTools) != len(beforeBindings[i].AllowedTools) {
			t.Fatalf("preview changed a binding's connection, classification, or tool policy")
		}
	}
}

// FR-73: a disabled binding is `Needs connection`, with the issue naming what
// to do about it.
func TestPreviewToolbox_ReportsNeedsConnection(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-1" {
			ws.MCPBindings[i].Enabled = false
		}
	}

	preview := previewFor(t, ws, "inst-1", lean.ID)
	if preview.Readiness != ReadinessNeedsConnection {
		t.Fatalf("expected Needs connection, got %q (%+v)", preview.Readiness, preview.Issues)
	}
	if preview.Focus.State != FocusNeedsAttention {
		t.Fatalf("expected a hard readiness failure to force Needs attention, got %q", preview.Focus.State)
	}
	if preview.CanUseDirectly {
		t.Fatalf("a toolbox that is not ready must never offer one-click use")
	}
	found := false
	for _, issue := range preview.Issues {
		if issue.Action == "connect" && issue.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a blocking connect action, got %+v", preview.Issues)
	}
}

// FR-73/FR-159: an unclassified operation is `Needs approval` — it would fail
// closed under a Goal's autonomy gate.
func TestPreviewToolbox_ReportsNeedsApprovalForUnclassifiedOperations(t *testing.T) {
	ws, _, _ := newUseFixture(t)
	ws.MCPBindings = append(ws.MCPBindings, MCPBinding{
		ID: "mb-raw", ServerName: "tracker", Enabled: true, AllowedTools: []string{"list_issues"},
	})
	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-raw",
		Name:        "Raw Kit",
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-raw", AllowedTools: []string{"list_issues"}, Required: true}},
	})
	if err != nil {
		t.Fatalf("CreateToolbox() error = %v", err)
	}

	preview := previewFor(t, ws, "inst-1", created.ID)
	if preview.Readiness != ReadinessNeedsApproval {
		t.Fatalf("expected Needs approval, got %q (%+v)", preview.Readiness, preview.Issues)
	}
}

// FR-77/FR-78: the diff is exact, and a switch that exposes something new
// routes through review.
func TestPreviewToolbox_ExpandingSwitchNeedsReview(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	preview := previewFor(t, ws, "inst-1", wide.ID)
	if !preview.ExpandsPermissions || preview.CanUseDirectly {
		t.Fatalf("expected a widening switch to require review, got expands=%v direct=%v", preview.ExpandsPermissions, preview.CanUseDirectly)
	}
	if preview.Diff == nil {
		t.Fatalf("expected a diff against the current assignment")
	}
	if len(preview.Diff.SkillsAdded) != 1 || preview.Diff.SkillsAdded[0].CapabilityID != "drafting" {
		t.Fatalf("expected drafting to be reported as added, got %+v", preview.Diff.SkillsAdded)
	}
}

// The mirror case: narrowing needs no review, because nothing is granted.
func TestPreviewToolbox_NarrowingSwitchIsDirectlyUsable(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: wide.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	preview := previewFor(t, ws, "inst-1", lean.ID)
	if preview.Readiness != ReadinessReady {
		t.Fatalf("expected Ready, got %q (%+v)", preview.Readiness, preview.Issues)
	}
	if preview.ExpandsPermissions || !preview.CanUseDirectly {
		t.Fatalf("expected a purely narrowing switch to be one-click, got expands=%v direct=%v", preview.ExpandsPermissions, preview.CanUseDirectly)
	}
}

// FR-78/FR-79: the server refuses an expanding switch that did not come
// through Review & Use. This is the check that keeps one-click incapable of
// granting anything new.
func TestUseToolbox_RefusesUnacknowledgedExpansion(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	_, err := UseToolbox(ws, ToolboxUseRequest{AgentInstanceID: "inst-1", ToolboxID: wide.ID},
		nil, 4, false, DefaultFocusThresholds())
	if !errors.Is(err, ErrToolboxUseNeedsReview) {
		t.Fatalf("expected an unacknowledged expansion to be refused, got %v", err)
	}
	// The prior assignment is untouched (FR-86).
	if assignment, _ := ws.GetToolboxAssignment("inst-1"); assignment.ToolboxID != lean.ID {
		t.Fatalf("expected the previous assignment to survive a refusal, got %q", assignment.ToolboxID)
	}

	result, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds())
	if err != nil {
		t.Fatalf("expected an acknowledged expansion to succeed, got %v", err)
	}
	if result.ToolboxID != wide.ID {
		t.Fatalf("expected the wide toolbox to be applied, got %q", result.ToolboxID)
	}
}

// FR-82: a write built against a stale workspace version is rejected.
func TestUseToolbox_RejectsStaleWorkspaceVersion(t *testing.T) {
	ws, lean, _ := newUseFixture(t)

	_, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: lean.ID, ExpectedWorkspaceVersion: 9999, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds())
	if !errors.Is(err, errStaleWorkspaceVersion) {
		t.Fatalf("expected a stale version to be rejected, got %v", err)
	}
}

// FR-83: readiness is revalidated at save time, not trusted from the preview.
func TestUseToolbox_RevalidatesReadinessAtSaveTime(t *testing.T) {
	ws, lean, _ := newUseFixture(t)

	// The user previewed while everything was connected...
	preview := previewFor(t, ws, "inst-1", lean.ID)
	if preview.Readiness != ReadinessReady {
		t.Fatalf("expected the preview to be Ready, got %q", preview.Readiness)
	}
	// ...and the binding went away before they clicked.
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-1" {
			ws.MCPBindings[i].Enabled = false
		}
	}

	_, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: lean.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds())
	if !errors.Is(err, ErrToolboxNotReady) {
		t.Fatalf("expected last-moment drift to block the write, got %v", err)
	}
}

// FR-84: exactly one instance changes, even when two share a name.
func TestUseToolbox_TouchesExactlyOneInstance(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	for _, instanceID := range []string{"inst-1", "inst-2"} {
		if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: instanceID, ToolboxID: lean.ID}); err != nil {
			t.Fatalf("SetToolboxAssignment(%s) error = %v", instanceID, err)
		}
	}

	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	first, _ := ws.GetToolboxAssignment("inst-1")
	second, _ := ws.GetToolboxAssignment("inst-2")
	if first.ToolboxID != wide.ID {
		t.Fatalf("expected instance 1 to switch, got %q", first.ToolboxID)
	}
	if second.ToolboxID != lean.ID {
		t.Fatalf("expected the same-named instance 2 to be untouched, got %q", second.ToolboxID)
	}
}

// FR-85: the assignment and the legacy access projection land together.
func TestUseToolbox_ProjectsLegacyAccessAlongsideTheAssignment(t *testing.T) {
	ws, _, wide := newUseFixture(t)

	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	skillAccess, ok := ws.GetAgentSkillAccess("inst-1")
	if !ok || len(skillAccess.EnabledBindingIDs) != 2 {
		t.Fatalf("expected the skill access projection to mirror the toolbox, got %+v", skillAccess)
	}
	mcpAccess, ok := ws.GetAgentMCPAccess("inst-1")
	if !ok || len(mcpAccess.EnabledBindingIDs) != 2 {
		t.Fatalf("expected the MCP access projection to mirror the toolbox, got %+v", mcpAccess)
	}
}

// FR-87: the receipt states what the user actually got.
func TestUseToolbox_ReturnsAReceipt(t *testing.T) {
	ws, _, wide := newUseFixture(t)

	result, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds())
	if err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	if result.AgentName != "Coder" || result.ToolboxName != "Wide Kit" || result.ToolboxVersion != 1 {
		t.Fatalf("expected an identifying receipt, got %+v", result)
	}
	if result.Focus.State == "" {
		t.Fatalf("expected the receipt to carry a Focus result")
	}
	if result.Permissions.Operations != 3 || result.Permissions.WriteOperations != 1 {
		t.Fatalf("expected a permission summary of 3 operations with 1 write, got %+v", result.Permissions)
	}
	if result.Capacity.Used != 2 {
		t.Fatalf("expected 2 skill spaces used, got %+v", result.Capacity)
	}
}

// FR-88: undo restores exactly what was displaced.
func TestUndoToolboxUse_RestoresThePriorAssignment(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	result, err := UndoToolboxUse(ws, "inst-1", 0, false, "tester", nil, 4, false, DefaultFocusThresholds())
	if err != nil {
		t.Fatalf("UndoToolboxUse() error = %v", err)
	}
	if result.ToolboxID != lean.ID {
		t.Fatalf("expected undo to restore the lean toolbox, got %q", result.ToolboxID)
	}
	// Undo narrows, so it needed no acknowledgement.
	if assignment, _ := ws.GetToolboxAssignment("inst-1"); assignment.ToolboxID != lean.ID {
		t.Fatalf("expected the restored assignment to be persisted, got %q", assignment.ToolboxID)
	}
}

// FR-90: undo is not a bypass. Restoring a version that would now widen
// permissions still requires review.
func TestUndoToolboxUse_RequiresReviewWhenRestoringWouldExpand(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: wide.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	// Switch DOWN to lean, so undoing would go back UP to wide.
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: lean.ID,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox(lean) error = %v", err)
	}

	_, err := UndoToolboxUse(ws, "inst-1", 0, false, "tester", nil, 4, false, DefaultFocusThresholds())
	if !errors.Is(err, ErrToolboxUseNeedsReview) {
		t.Fatalf("expected an expanding undo to require Review & Restore, got %v", err)
	}
	if assignment, _ := ws.GetToolboxAssignment("inst-1"); assignment.ToolboxID != lean.ID {
		t.Fatalf("expected the refused undo to leave the current assignment intact, got %q", assignment.ToolboxID)
	}
}

func TestUndoToolboxUse_NothingToUndo(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	if _, err := UndoToolboxUse(ws, "inst-1", 0, false, "tester", nil, 4, false, DefaultFocusThresholds()); err == nil {
		t.Fatalf("expected undo with no prior assignment to fail cleanly")
	}
}

// FR-33/FR-56: a selection past capacity is `Toolbox full` and cannot be used.
func TestPreviewToolbox_ToolboxFullBlocksUse(t *testing.T) {
	ws, _, wide := newUseFixture(t)
	instance := findAgentInstanceByID(ws, "inst-1")
	definition, _ := ws.GetToolbox(wide.ID)

	preview := PreviewToolbox(ws, instance, *definition, definition.CurrentRecipe(), nil, 1, false, DefaultFocusThresholds())
	preview.ApplyCurrentAssignmentDiff(ws)

	if preview.Readiness != ReadinessToolboxFull {
		t.Fatalf("expected Toolbox full, got %q (%+v)", preview.Readiness, preview.Issues)
	}

	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 1, false, DefaultFocusThresholds()); !errors.Is(err, ErrToolboxNotReady) {
		t.Fatalf("expected an over-capacity toolbox to be unusable, got %v", err)
	}

	// FR-60: expert mode lifts the ceiling.
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 1, true, DefaultFocusThresholds()); err != nil {
		t.Fatalf("expected expert mode to permit the switch, got %v", err)
	}
}

// FR-20: an archived toolbox cannot be newly selected.
func TestUseToolbox_RefusesAnArchivedToolbox(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxStatus(lean.ID, ToolboxStatusArchived); err != nil {
		t.Fatalf("SetToolboxStatus() error = %v", err)
	}

	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: lean.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err == nil {
		t.Fatalf("expected an archived toolbox to be unusable")
	}
}
