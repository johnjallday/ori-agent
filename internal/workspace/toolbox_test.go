package workspace

import (
	"errors"
	"strings"
	"testing"
)

// Domain and persistence coverage for named Toolboxes (task 1.15; PRD
// FR-1–FR-30).

func newToolboxTestWorkspace() *Workspace {
	return &Workspace{
		ID: "ws-toolbox",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note", "write_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
	}
}

func mustCreateToolbox(t *testing.T, ws *Workspace, def ToolboxDefinition) *ToolboxDefinition {
	t.Helper()
	created, err := ws.CreateToolbox(def)
	if err != nil {
		t.Fatalf("CreateToolbox(%s) error = %v", def.Name, err)
	}
	return created
}

func TestNormalizeToolboxContent_DeduplicatesAndOrders(t *testing.T) {
	skills, bindings := NormalizeToolboxContent(
		[]ToolboxSkillRef{
			{CapabilityID: "  Zeta  ", Source: ToolboxSourceAgentLearned},
			{DisplayName: "Alpha", Source: "AGENT_LEARNED"},
			{CapabilityID: "zeta", DisplayName: "Zeta", Source: ToolboxSourceAgentLearned},
		},
		[]ToolboxMCPRef{
			{BindingID: "mb-2", AllowedTools: []string{"b", "a", " a "}},
			{BindingID: "MB-2", AllowedTools: []string{"c"}, Required: true},
			{BindingID: "mb-1", AllowedTools: []string{}},
		},
	)

	if len(skills) != 2 {
		t.Fatalf("expected case-insensitive skill deduplication, got %+v", skills)
	}
	if skills[0].CapabilityID != "alpha" || skills[1].CapabilityID != "zeta" {
		t.Fatalf("expected deterministic skill ordering, got %+v", skills)
	}
	if skills[0].DisplayName != "Alpha" {
		t.Fatalf("expected the exact-case display name to be preserved, got %q", skills[0].DisplayName)
	}

	if len(bindings) != 2 {
		t.Fatalf("expected the duplicate binding to merge, got %+v", bindings)
	}
	if bindings[0].BindingID != "mb-1" || bindings[1].BindingID != "mb-2" {
		t.Fatalf("expected deterministic binding ordering, got %+v", bindings)
	}
	if got := strings.Join(bindings[1].AllowedTools, ","); got != "a,b,c" {
		t.Fatalf("expected merged binding tools to be the deduplicated union, got %q", got)
	}
	if !bindings[1].Required {
		t.Fatalf("expected the stricter Required claim to win the merge")
	}
	if bindings[0].AllowedTools == nil {
		t.Fatalf("expected an explicit empty tool list to stay non-nil (a real 'no operations' selection)")
	}
}

func TestValidateToolboxContent_RejectsSourceCollisionAndAllToolsSemantics(t *testing.T) {
	skills, bindings := NormalizeToolboxContent(
		[]ToolboxSkillRef{
			{CapabilityID: "review", Source: ToolboxSourceAgentLearned},
			{CapabilityID: "Review", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
		},
		nil,
	)
	err := ValidateToolboxContent(skills, bindings)
	if !errors.Is(err, ErrToolboxSourceCollision) {
		t.Fatalf("expected a source collision to be surfaced, got %v", err)
	}

	_, allTools := NormalizeToolboxContent(nil, []ToolboxMCPRef{{BindingID: "mb-2"}})
	if err := ValidateToolboxContent(nil, allTools); !errors.Is(err, ErrToolboxAllToolsSemantics) {
		t.Fatalf("expected legacy all-tools semantics to be rejected, got %v", err)
	}

	_, migrated := NormalizeToolboxContent(nil, []ToolboxMCPRef{{BindingID: "mb-2", InheritsBindingTools: true}})
	if err := ValidateToolboxContent(nil, migrated); err != nil {
		t.Fatalf("expected a migrated inherited-tools entry to remain valid, got %v", err)
	}
}

func TestCreateToolbox_RejectsDuplicateNameCaseInsensitively(t *testing.T) {
	ws := newToolboxTestWorkspace()
	mustCreateToolbox(t, ws, ToolboxDefinition{ID: "tbx-1", Name: "Research Kit"})

	_, err := ws.CreateToolbox(ToolboxDefinition{ID: "tbx-2", Name: "  research   kit  "})
	if !errors.Is(err, ErrToolboxNameTaken) {
		t.Fatalf("expected a case-insensitive duplicate name to be rejected, got %v", err)
	}
}

func TestValidateToolboxAgainstWorkshop_ForbidsWideningAllowedTools(t *testing.T) {
	ws := newToolboxTestWorkspace()

	_, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-widen",
		Name:        "Too Much",
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note", "delete_note"}}},
	})
	if !errors.Is(err, ErrToolboxWidensAllowedTools) {
		t.Fatalf("expected widening the binding's allowed tools to be rejected, got %v", err)
	}

	if _, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-narrow",
		Name:        "Just Enough",
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}},
	}); err != nil {
		t.Fatalf("expected narrowing the binding's allowed tools to be permitted, got %v", err)
	}
}

func TestSaveToolboxVersion_KeepsHistoricalVersionsImmutable(t *testing.T) {
	ws := newToolboxTestWorkspace()
	created := mustCreateToolbox(t, ws, ToolboxDefinition{
		ID:     "tbx-1",
		Name:   "Research Kit",
		Skills: []ToolboxSkillRef{{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
	})
	if created.Version != 1 {
		t.Fatalf("expected a new toolbox to start at version 1, got %d", created.Version)
	}

	updated, err := ws.SaveToolboxVersion("tbx-1",
		[]ToolboxSkillRef{{CapabilityID: "drafting", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-2"}},
		nil, ToolboxProvenanceUser, "tester")
	if err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected an edit to produce version 2, got %d", updated.Version)
	}

	v1, err := updated.ResolveVersion(1)
	if err != nil {
		t.Fatalf("expected version 1 to remain resolvable after the edit: %v", err)
	}
	if len(v1.Skills) != 1 || v1.Skills[0].CapabilityID != "testing" {
		t.Fatalf("expected version 1 to keep its original meaning, got %+v", v1.Skills)
	}

	v2, err := updated.ResolveVersion(2)
	if err != nil {
		t.Fatalf("ResolveVersion(2) error = %v", err)
	}
	if len(v2.Skills) != 1 || v2.Skills[0].CapabilityID != "drafting" {
		t.Fatalf("expected version 2 to carry the edit, got %+v", v2.Skills)
	}

	if _, err := updated.ResolveVersion(99); !errors.Is(err, ErrToolboxVersionNotFound) {
		t.Fatalf("expected an unknown version to be reported, got %v", err)
	}
}

func TestSetToolboxAssignment_IsPerInstanceAndRetainsPriorForUndo(t *testing.T) {
	ws := newToolboxTestWorkspace()
	research := mustCreateToolbox(t, ws, ToolboxDefinition{ID: "tbx-research", Name: "Research Kit"})
	writing := mustCreateToolbox(t, ws, ToolboxDefinition{ID: "tbx-writing", Name: "Writing Kit"})

	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: research.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment(inst-1) error = %v", err)
	}
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-2", ToolboxID: writing.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment(inst-2) error = %v", err)
	}

	// Two instances of the SAME reusable agent carry different toolboxes.
	first, _ := ws.GetToolboxAssignment("inst-1")
	second, _ := ws.GetToolboxAssignment("inst-2")
	if first.ToolboxID != research.ID || second.ToolboxID != writing.ID {
		t.Fatalf("expected per-instance assignments, got %q and %q", first.ToolboxID, second.ToolboxID)
	}

	switched, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: writing.ID})
	if err != nil {
		t.Fatalf("SetToolboxAssignment(switch) error = %v", err)
	}
	if switched.Previous == nil || switched.Previous.ToolboxID != research.ID {
		t.Fatalf("expected the displaced assignment to be retained for undo, got %+v", switched.Previous)
	}
	// The other instance is untouched.
	if after, _ := ws.GetToolboxAssignment("inst-2"); after.ToolboxID != writing.ID || after.Previous != nil {
		t.Fatalf("expected instance 2 to be unaffected by instance 1's switch, got %+v", after)
	}
}

func TestSetToolboxAssignment_RejectsArchivedAndForeignInstances(t *testing.T) {
	ws := newToolboxTestWorkspace()
	created := mustCreateToolbox(t, ws, ToolboxDefinition{ID: "tbx-1", Name: "Research Kit"})

	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "not-here", ToolboxID: created.ID}); err == nil {
		t.Fatalf("expected assigning to an instance outside the workspace to fail")
	}

	if _, err := ws.SetToolboxStatus(created.ID, ToolboxStatusArchived); err != nil {
		t.Fatalf("SetToolboxStatus() error = %v", err)
	}
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID}); !errors.Is(err, ErrToolboxArchived) {
		t.Fatalf("expected an archived toolbox to be unselectable, got %v", err)
	}
}

func TestDeleteToolbox_BlockedWhileAssignedAndListsReferences(t *testing.T) {
	ws := newToolboxTestWorkspace()
	created := mustCreateToolbox(t, ws, ToolboxDefinition{ID: "tbx-1", Name: "Research Kit"})
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	refs := ws.ToolboxReferences(created.ID)
	if len(refs) != 1 || refs[0].Kind != "assignment" || refs[0].ID != "inst-1" {
		t.Fatalf("expected the active assignment to be reported as a reference, got %+v", refs)
	}
	if err := ws.DeleteToolbox(created.ID); err == nil {
		t.Fatalf("expected deleting an assigned toolbox to be blocked")
	}

	if err := ws.DeleteToolboxAssignment("inst-1"); err != nil {
		t.Fatalf("DeleteToolboxAssignment() error = %v", err)
	}
	if err := ws.DeleteToolbox(created.ID); err != nil {
		t.Fatalf("expected an unreferenced toolbox to be deletable, got %v", err)
	}
}

func TestResolveAssignedToolbox_ReturnsThePinnedVersionNotTheLatest(t *testing.T) {
	ws := newToolboxTestWorkspace()
	created := mustCreateToolbox(t, ws, ToolboxDefinition{
		ID:     "tbx-1",
		Name:   "Research Kit",
		Skills: []ToolboxSkillRef{{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
	})
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID, ToolboxVersion: 1}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	if _, err := ws.SaveToolboxVersion(created.ID,
		[]ToolboxSkillRef{
			{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
			{CapabilityID: "drafting", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-2"},
		}, nil, ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}

	_, recipe, ok, err := ws.ResolveAssignedToolbox("inst-1")
	if err != nil || !ok {
		t.Fatalf("ResolveAssignedToolbox() ok=%v err=%v", ok, err)
	}
	if recipe.Version != 1 || len(recipe.Skills) != 1 {
		t.Fatalf("expected the pinned version 1 to survive a later edit, got version %d with %d skills", recipe.Version, len(recipe.Skills))
	}
}

func TestSkillSpacesUsed_ExcludesCoreAndDeduplicates(t *testing.T) {
	definition := ToolboxDefinition{
		Skills: []ToolboxSkillRef{
			{CapabilityID: "testing", Source: ToolboxSourceAgentLearned},
			{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"},
			{CapabilityID: "workspace-settings", Source: ToolboxSourceCore},
		},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"a", "b", "c"}}},
	}
	if got := definition.SkillSpacesUsed(); got != 1 {
		t.Fatalf("expected one space-consuming skill (core excluded, identity deduplicated), got %d", got)
	}
}

func TestEvaluateToolboxCapacity_ReportsFullAndGrandfathered(t *testing.T) {
	three := []ToolboxSkillRef{
		{CapabilityID: "a", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "b", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "c", Source: ToolboxSourceAgentLearned},
	}

	atCap := EvaluateToolboxCapacity(three[:2], 2, false)
	if !atCap.Full || atCap.Grandfathered {
		t.Fatalf("expected a toolbox at capacity to be full but not grandfathered, got %+v", atCap)
	}

	over := EvaluateToolboxCapacity(three, 2, false)
	if !over.Full || !over.Grandfathered {
		t.Fatalf("expected an over-capacity toolbox to be full and grandfathered, got %+v", over)
	}

	expert := EvaluateToolboxCapacity(three, 2, true)
	if expert.Full || expert.Grandfathered {
		t.Fatalf("expected expert mode to lift the cap, got %+v", expert)
	}

	unresolvable := EvaluateToolboxCapacity(three, 0, false)
	if unresolvable.Full {
		t.Fatalf("expected an unresolvable capacity to enforce nothing, got %+v", unresolvable)
	}
}

// FR-33: a grandfathered over-capacity toolbox keeps everything it has and may
// still be edited — only ADDING another space-consuming skill is blocked.
func TestEnforceToolboxCapacity_BlocksAdditionsButNotEdits(t *testing.T) {
	current := []ToolboxSkillRef{
		{CapabilityID: "a", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "b", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "c", Source: ToolboxSourceAgentLearned},
	}

	// Removing from an over-capacity toolbox is always allowed.
	if err := EnforceToolboxCapacity(current, current[:2], 2, false); err != nil {
		t.Fatalf("expected removal from a grandfathered toolbox to be permitted, got %v", err)
	}
	// Swapping without growing is allowed even while over capacity.
	swapped := []ToolboxSkillRef{
		{CapabilityID: "a", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "b", Source: ToolboxSourceAgentLearned},
		{CapabilityID: "d", Source: ToolboxSourceAgentLearned},
	}
	if err := EnforceToolboxCapacity(current, swapped, 2, false); err != nil {
		t.Fatalf("expected a same-size edit of a grandfathered toolbox to be permitted, got %v", err)
	}
	// Growing past the cap is not.
	grown := append(append([]ToolboxSkillRef(nil), current...), ToolboxSkillRef{CapabilityID: "d", Source: ToolboxSourceAgentLearned})
	if err := EnforceToolboxCapacity(current, grown, 2, false); !errors.Is(err, ErrToolboxFull) {
		t.Fatalf("expected growing a full toolbox to be blocked, got %v", err)
	}
	// Expert mode lifts the ceiling.
	if err := EnforceToolboxCapacity(current, grown, 2, true); err != nil {
		t.Fatalf("expected expert mode to permit the addition, got %v", err)
	}
	// Core capabilities never consume a space.
	withCore := append(append([]ToolboxSkillRef(nil), current...), ToolboxSkillRef{CapabilityID: "workspace-settings", Source: ToolboxSourceCore})
	if err := EnforceToolboxCapacity(current, withCore, 2, false); err != nil {
		t.Fatalf("expected adding a core capability to be permitted, got %v", err)
	}
}

func TestToolboxPersistence_RoundTripsThroughTheStore(t *testing.T) {
	ws := newToolboxTestWorkspace()
	created := mustCreateToolbox(t, ws, ToolboxDefinition{
		ID:          "tbx-1",
		Name:        "Research Kit",
		Skills:      []ToolboxSkillRef{{CapabilityID: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}},
	})
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	store := newTestWorkspaceStore(t, ws)
	reloaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}

	definition, recipe, ok, err := reloaded.ResolveAssignedToolbox("inst-1")
	if err != nil || !ok {
		t.Fatalf("ResolveAssignedToolbox() after reload ok=%v err=%v", ok, err)
	}
	if definition.Name != "Research Kit" || len(recipe.Skills) != 1 || len(recipe.MCPBindings) != 1 {
		t.Fatalf("expected the toolbox to survive persistence, got %+v / %+v", definition, recipe)
	}
	if recipe.MCPBindings[0].AllowedTools[0] != "read_note" {
		t.Fatalf("expected the exact tool selection to survive persistence, got %+v", recipe.MCPBindings[0])
	}
}
