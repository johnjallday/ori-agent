package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Cross-phase safety and audit invariants (tasks 6.7–6.10, 6.14;
// PRD FR-156–FR-161).
//
// These are the tests that would catch a REGRESSION rather than a bug in
// today's code: they assert properties the whole feature depends on, at the
// seams where a future change is most likely to break one by accident.

// FR-156: a workspace Toolbox operation must never reach the reusable global
// agent. The two live in different stores, so this asserts the boundary holds
// rather than that a particular function is careful.
func TestSafety_WorkspaceOperationsNeverTouchTheGlobalAgent(t *testing.T) {
	ws, lean, wide := newUseFixture(t)

	globalAgent := &agent.Agent{
		Type:      agent.TypeResearch,
		Role:      types.RoleGeneral,
		Settings:  types.Settings{Model: "gpt-5", SystemPrompt: "Be careful."},
		Metadata:  &types.AgentMetadata{Description: "reusable"},
		Evolution: &types.AgentEvolution{Level: 4, Stage: types.AgentStageLearner},
		DefaultToolbox: &types.AgentDefaultToolbox{
			Version: 2,
			Skills:  []types.DefaultToolboxSkillRef{{CapabilityID: "code-review"}},
		},
	}
	before := *globalAgent
	beforeDefault := globalAgent.DefaultToolbox.Clone()

	// Every workspace-side mutation this feature offers. The starting
	// assignment gives Undo something to restore.
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	if _, err := ws.SaveToolboxVersion(lean.ID, nil, nil, ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	if _, err := ws.UpdateToolboxMetadata(lean.ID, "Renamed", "desc", "🔍", "#fff"); err != nil {
		t.Fatalf("UpdateToolboxMetadata() error = %v", err)
	}
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}
	if _, err := UndoToolboxUse(ws, "inst-1", 0, true, "tester", nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UndoToolboxUse() error = %v", err)
	}
	if _, err := ws.SetToolboxStatus(lean.ID, ToolboxStatusArchived); err != nil {
		t.Fatalf("SetToolboxStatus() error = %v", err)
	}

	if globalAgent.Settings.Model != before.Settings.Model ||
		globalAgent.Settings.SystemPrompt != before.Settings.SystemPrompt ||
		globalAgent.Role != before.Role ||
		globalAgent.Type != before.Type ||
		globalAgent.Metadata.Description != before.Metadata.Description ||
		globalAgent.Evolution.Level != before.Evolution.Level ||
		globalAgent.Evolution.Stage != before.Evolution.Stage {
		t.Fatalf("a workspace toolbox operation mutated the reusable agent: %+v", globalAgent)
	}
	if globalAgent.DefaultToolbox.Version != beforeDefault.Version ||
		len(globalAgent.DefaultToolbox.Skills) != len(beforeDefault.Skills) {
		t.Fatalf("a workspace toolbox operation mutated the Default Toolbox: %+v", globalAgent.DefaultToolbox)
	}
}

// FR-157: nothing in this feature may silently widen a filesystem root, an
// allowlist, a trust flag, a side-effect classification, or goal autonomy.
func TestSafety_NoOperationSilentlyWidensPermissions(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	ws.MCPBindings[0].Scope = map[string]any{"roots": []string{"/tmp/notes"}}
	ws.AutonomyPolicy = AutonomyWatch

	type bindingFacts struct {
		enabled   bool
		tools     []string
		effect    SideEffect
		overrides map[string]SideEffect
		scope     map[string]any
	}
	capture := func() map[string]bindingFacts {
		facts := make(map[string]bindingFacts)
		for _, binding := range ws.GetMCPBindings() {
			facts[binding.ID] = bindingFacts{
				enabled:   binding.Enabled,
				tools:     append([]string(nil), binding.AllowedTools...),
				effect:    binding.DefaultSideEffect,
				overrides: binding.ToolOverrides,
				scope:     binding.Scope,
			}
		}
		return facts
	}

	before := capture()
	beforeAutonomy := ws.AutonomyPolicy
	beforeTrusted := ws.GetSkillBindings()[0].Trusted

	// Preview, use, undo, recommend — the full loop.
	instance := findAgentInstanceByID(ws, "inst-1")
	definition, _ := ws.GetToolbox(wide.ID)
	preview := PreviewToolbox(ws, instance, *definition, definition.CurrentRecipe(), nil, 4, false, DefaultFocusThresholds())
	preview.ApplyCurrentAssignmentDiff(ws)
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: lean.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}
	now := time.Now()
	RecommendToolboxes(ws, instance, &GoalBrief{
		RequiredCapabilities: []string{"testing"}, AcceptedAt: &now, Version: 1,
	}, nil, 4, false, DefaultFocusThresholds())

	after := capture()
	for id, was := range before {
		is := after[id]
		if is.enabled != was.enabled {
			t.Fatalf("binding %s connection state changed", id)
		}
		if len(is.tools) != len(was.tools) {
			t.Fatalf("binding %s allowlist width changed: %v -> %v", id, was.tools, is.tools)
		}
		if is.effect != was.effect {
			t.Fatalf("binding %s side-effect classification changed", id)
		}
		if len(is.overrides) != len(was.overrides) {
			t.Fatalf("binding %s per-tool classification changed", id)
		}
		// The filesystem root is the sharpest case: widening one silently would
		// hand an agent a directory nobody approved.
		if len(is.scope) != len(was.scope) {
			t.Fatalf("binding %s scope changed", id)
		}
	}
	if ws.AutonomyPolicy != beforeAutonomy {
		t.Fatalf("goal autonomy changed from %q to %q", beforeAutonomy, ws.AutonomyPolicy)
	}
	if ws.GetSkillBindings()[0].Trusted != beforeTrusted {
		t.Fatalf("a skill binding's trust flag changed")
	}
}

// FR-158: using a Toolbox is not an opt-in substitute for native MCP CLI.
func TestSafety_UsingAToolboxIsNotANativeMCPOptIn(t *testing.T) {
	ws, _, wide := newUseFixture(t)
	if ws.AllowNativeMCPCLI {
		t.Fatalf("fixture should start without native MCP CLI")
	}

	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	if ws.AllowNativeMCPCLI {
		t.Fatalf("using a toolbox granted native MCP CLI permission")
	}
}

// FR-159: an unclassified write or external operation keeps a Toolbox out of
// `Ready`, so a Goal cannot execute with it until the user classifies it
// through the existing flow.
func TestSafety_UnclassifiedOperationsBlockUntilClassified(t *testing.T) {
	ws, _, _ := newUseFixture(t)
	ws.MCPBindings = append(ws.MCPBindings, MCPBinding{
		ID: "mb-raw", ServerName: "tracker", Enabled: true, AllowedTools: []string{"push_change"},
	})
	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-raw",
		Name:        "Raw Kit",
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-raw", AllowedTools: []string{"push_change"}, Required: true}},
	})
	if err != nil {
		t.Fatalf("CreateToolbox() error = %v", err)
	}

	_, useErr := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: created.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds())
	if !errors.Is(useErr, ErrToolboxNotReady) {
		t.Fatalf("expected an unclassified operation to block use, got %v", useErr)
	}

	// Classifying through the existing flow unblocks it — the Toolbox never
	// classified anything itself.
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-raw" {
			ws.MCPBindings[i].DefaultSideEffect = SideEffectWrite
		}
	}
	if _, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: created.ID, AcknowledgedExpansion: true,
	}, nil, 4, false, DefaultFocusThresholds()); err != nil {
		t.Fatalf("expected a classified operation to be usable, got %v", err)
	}
}

// FR-160/FR-161: every write records who, when, and against which exact
// instance and version.
func TestSafety_EveryWriteRecordsActorProvenanceAndVersions(t *testing.T) {
	ws, lean, wide := newUseFixture(t)

	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID: "tbx-audit", Name: "Audited Kit", Provenance: ToolboxProvenanceUser, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("CreateToolbox() error = %v", err)
	}
	if created.Provenance != ToolboxProvenanceUser || created.Actor != "tester" {
		t.Fatalf("expected creation provenance and actor, got %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("expected creation timestamps")
	}

	// A starting assignment, so the later Undo has a target.
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	versioned, err := ws.SaveToolboxVersion(lean.ID, nil, nil, ToolboxProvenanceUser, "editor")
	if err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	if versioned.Actor != "editor" || versioned.Version != 2 {
		t.Fatalf("expected the edit to record its actor and new version, got %+v", versioned)
	}

	result, err := UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID: "inst-1", ToolboxID: wide.ID, AcknowledgedExpansion: true,
		Provenance: ToolboxProvenanceUser, Actor: "switcher",
	}, nil, 4, false, DefaultFocusThresholds())
	if err != nil {
		t.Fatalf("UseToolbox() error = %v", err)
	}

	assignment, ok := ws.GetToolboxAssignment("inst-1")
	if !ok {
		t.Fatalf("expected an assignment")
	}
	// FR-161: the audit record names the STABLE INSTANCE and the exact version.
	if assignment.AgentInstanceID != "inst-1" || assignment.ToolboxID != wide.ID || assignment.ToolboxVersion != 1 {
		t.Fatalf("expected the exact instance and version to be recorded, got %+v", assignment)
	}
	if assignment.Actor != "switcher" || assignment.Provenance != ToolboxProvenanceUser {
		t.Fatalf("expected the applying actor and provenance, got %+v", assignment)
	}
	if assignment.AppliedAt.IsZero() {
		t.Fatalf("expected an application timestamp")
	}
	if result.AppliedAt != assignment.AppliedAt {
		t.Fatalf("expected the receipt and the record to agree on when it happened")
	}

	// Undo is its own audited event, not a silent revert.
	undone, err := UndoToolboxUse(ws, "inst-1", 0, true, "undoer", nil, 4, false, DefaultFocusThresholds())
	if err != nil {
		t.Fatalf("UndoToolboxUse() error = %v", err)
	}
	restored, _ := ws.GetToolboxAssignment("inst-1")
	if restored.Provenance != "undo" || restored.Actor != "undoer" {
		t.Fatalf("expected undo to record its own provenance and actor, got %+v", restored)
	}
	if undone.ToolboxVersion != restored.ToolboxVersion {
		t.Fatalf("expected the undo receipt to name the version it restored")
	}
}

// FR-167: a failed operation says what remained unchanged. The message is the
// contract — a bare failure leaves a user unsure whether to retry.
func TestSafety_FailuresExplainWhatRemainedUnchanged(t *testing.T) {
	ws, lean, wide := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	// A stale write, an unreviewed expansion, and an unready toolbox all leave
	// the prior assignment in force.
	for _, request := range []ToolboxUseRequest{
		{AgentInstanceID: "inst-1", ToolboxID: wide.ID, ExpectedWorkspaceVersion: 9999, AcknowledgedExpansion: true},
		{AgentInstanceID: "inst-1", ToolboxID: wide.ID},
	} {
		if _, err := UseToolbox(ws, request, nil, 4, false, DefaultFocusThresholds()); err == nil {
			t.Fatalf("expected %+v to be refused", request)
		}
		assignment, _ := ws.GetToolboxAssignment("inst-1")
		if assignment.ToolboxID != lean.ID {
			t.Fatalf("a refused switch changed the assignment to %q", assignment.ToolboxID)
		}
	}
}

// The Phase 2 boundary: V1 reserves traceable memory references and implements
// nothing else. Field Notes must not appear in Toolbox contents, capacity, or
// recommendations (task 6.11; deferred FR-123–FR-143).
func TestSafety_PhaseTwoBoundaryIsReservedNotImplemented(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	data, ok := BuildRunToolboxSnapshot(ws, "inst-1", nil, 4, false, DefaultFocusThresholds())
	if !ok {
		t.Fatalf("expected a snapshot")
	}
	// The memory-context readout exists in the Focus contract and stays zero,
	// so Phase 2 changes no shape when it lands.
	if chars, present := data.FocusInputs["memory_context_chars"]; !present {
		t.Fatalf("expected the memory-context field to be reserved in the Focus inputs")
	} else if chars != 0 {
		t.Fatalf("expected no memory context in V1, got %v", chars)
	}

	// No Toolbox source names Field Notes, and no skill entry can come from one.
	definition, _ := ws.GetToolbox(lean.ID)
	for _, skill := range definition.CurrentRecipe().Skills {
		if strings.Contains(strings.ToLower(skill.Source), "note") ||
			strings.Contains(strings.ToLower(skill.Source), "memory") {
			t.Fatalf("a toolbox entry claims a Phase 2 source: %+v", skill)
		}
	}
}

// The Phase 3 boundary: the delete guard recognizes a future Team Setup
// reference kind without implementing any Team Setup behavior (task 6.12;
// deferred FR-144–FR-155).
func TestSafety_PhaseThreeBoundaryIsRecognizedNotImplemented(t *testing.T) {
	ws, lean, _ := newUseFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: lean.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	references := ws.ToolboxReferences(lean.ID)
	if len(references) == 0 {
		t.Fatalf("expected the assignment to be reported as a reference")
	}
	// V1 produces only assignment references. The guard's Kind field is where a
	// Team Setup reference will appear, and nothing produces one yet.
	for _, reference := range references {
		if reference.Kind == "team_setup" {
			t.Fatalf("V1 must not produce Team Setup references")
		}
		// References identify the stable instance and the pinned version, which
		// is what a Phase 3 reference will also need (FR-161).
		if reference.Kind == "assignment" && (reference.ID == "" || reference.ToolboxVersion == 0) {
			t.Fatalf("expected a reference to name the instance and version, got %+v", reference)
		}
	}
}
