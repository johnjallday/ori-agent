package workspace

import (
	"testing"
)

// Snapshot construction from live workspace state (task 5.15; PRD FR-107,
// FR-108).

func TestBuildRunToolboxSnapshot_CapturesExactCapabilities(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	data, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds())
	if !ok {
		t.Fatalf("expected a snapshot for an assigned instance")
	}

	if data.WorkspaceID != ws.ID || data.WorkspaceVersion != ws.Version {
		t.Fatalf("expected the workspace identity and version, got %+v", data)
	}
	if data.AgentInstanceID != "inst-1" || data.AgentName != "Coder" {
		t.Fatalf("expected the stable instance identity, got %+v", data)
	}
	if data.ToolboxID != lean.ID || data.ToolboxVersion != 1 {
		t.Fatalf("expected the pinned toolbox version, got %+v", data)
	}
	if len(data.Skills) != 1 || data.Skills[0].CapabilityID != "testing" {
		t.Fatalf("expected the effective skill, got %+v", data.Skills)
	}
	if len(data.MCPBindings) != 1 {
		t.Fatalf("expected one binding, got %+v", data.MCPBindings)
	}

	binding := data.MCPBindings[0]
	if len(binding.AllowedTools) != 1 || binding.AllowedTools[0] != "read_note" {
		t.Fatalf("expected the exact operation subset, got %v", binding.AllowedTools)
	}
	// The materialized name is the join key the Wrap-up uses to attribute a
	// tool call back to its binding.
	want := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	if binding.RuntimeServerName != want {
		t.Fatalf("runtime server name = %q, want %q", binding.RuntimeServerName, want)
	}
	if binding.DefaultSideEffect != string(SideEffectRead) {
		t.Fatalf("expected the side-effect classification, got %q", binding.DefaultSideEffect)
	}
	if data.FocusState == "" || data.FocusInputs == nil {
		t.Fatalf("expected the Focus assessment and its inputs, got %+v", data)
	}
	if data.AutonomyPolicy != string(ws.AutonomyPolicy) {
		t.Fatalf("expected the autonomy policy in force, got %q", data.AutonomyPolicy)
	}
}

// An unavailable capability contributed nothing at runtime, so recording it
// would overstate what the run had (FR-14).
func TestBuildRunToolboxSnapshot_ExcludesUnavailableCapabilities(t *testing.T) {
	ws, _, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: wide.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-2" {
			ws.MCPBindings[i].Enabled = false
		}
	}

	data, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds())
	if !ok {
		t.Fatalf("expected a snapshot")
	}
	for _, binding := range data.MCPBindings {
		if binding.BindingID == "mb-2" {
			t.Fatalf("expected a disconnected binding to be excluded from the snapshot")
		}
	}
}

// FR-103/FR-104: a Goal's pin wins over the instance's current assignment, so
// a recurring goal reproduces regardless of what the agent switched to.
func TestBuildRunToolboxSnapshot_GoalPinWinsOverCurrentAssignment(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: wide.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	ws.GoalToolboxPolicy = &GoalToolboxPolicy{
		EntryAgentInstanceID: "inst-1",
		ToolboxID:            lean.ID,
		ToolboxVersion:       1,
	}

	data, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds())
	if !ok {
		t.Fatalf("expected a snapshot")
	}
	if data.ToolboxID != lean.ID || !data.PinnedByGoal {
		t.Fatalf("expected the goal's pin to win, got %+v", data)
	}
}

// A broken pin produces NO snapshot rather than quietly falling back to the
// current assignment — the preflight reports it, and running with something
// other than what was pinned would be worse than not running (FR-105).
func TestBuildRunToolboxSnapshot_BrokenPinDoesNotFallBack(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: wide.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	ws.GoalToolboxPolicy = &GoalToolboxPolicy{
		EntryAgentInstanceID: "inst-1",
		ToolboxID:            lean.ID,
		ToolboxVersion:       99, // no such version
	}

	if _, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds()); ok {
		t.Fatalf("expected a broken pin to produce no snapshot rather than a substitute")
	}
}

// An instance with no explicit assignment yields no snapshot: absent means
// "unknown", never "unrestricted".
func TestBuildRunToolboxSnapshot_UnassignedInstanceHasNone(t *testing.T) {
	ws, _, _ := newUseFixture(t)

	if _, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds()); ok {
		t.Fatalf("expected no snapshot for an unassigned instance")
	}
	if _, ok := BuildRunToolboxSnapshot(ws, "not-here", nil, 4, false, DefaultFocusThresholds()); ok {
		t.Fatalf("expected no snapshot for an unknown instance")
	}
}

// FR-110: a snapshot taken now is unaffected by a later edit — the whole point
// of denormalizing it.
func TestBuildRunToolboxSnapshot_SurvivesALaterToolboxEdit(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	before, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds())
	if !ok {
		t.Fatalf("expected a snapshot")
	}

	// The toolbox grows after the run started.
	if _, err := ws.SaveToolboxVersion(lean.ID,
		[]ToolboxSkillRef{
			{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
			{CapabilityID: "drafting", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-2"},
		},
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note", "write_note"}}},
		ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}

	if len(before.Skills) != 1 || len(before.MCPBindings[0].AllowedTools) != 1 {
		t.Fatalf("expected the already-taken snapshot to be unchanged, got %+v", before)
	}
}
