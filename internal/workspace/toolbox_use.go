package workspace

import (
	"fmt"
	"strings"
	"time"
)

// Using a Toolbox: one atomic, server-owned, rollback-safe operation
// (PRD FR-81–FR-91, §9.5).
//
// The pre-Toolbox editor applied a selection by issuing one request per
// capability from the browser. That is not atomic and cannot be made atomic: a
// failure halfway through left an agent with some of the new capabilities and
// some of the old, and there was no record of what the state had been. The
// whole point of this file is that a switch either happens completely or not at
// all, and that the displaced assignment is kept so it can be undone.
//
// Three guarantees hold across everything here:
//
//   - Exactly one AgentInstance.ID is touched. Never every instance sharing a
//     name (FR-84) — that bug is why assignments are keyed by ID.
//   - Hard readiness is revalidated on the SERVER immediately before persisting
//     (FR-83). A preview is a moment old by the time a user clicks; the world
//     may have changed, and consent given against a stale preview is not
//     consent to what would actually happen.
//   - A failure leaves the previous effective Toolbox intact (FR-86). The
//     mutation runs against a workspace loaded inside store.Update, and an
//     error means nothing is saved.

// ToolboxUseResult is the user-visible receipt for a successful switch (FR-87).
type ToolboxUseResult struct {
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name,omitempty"`
	ToolboxID       string `json:"toolbox_id"`
	ToolboxName     string `json:"toolbox_name"`
	ToolboxVersion  int64  `json:"toolbox_version"`

	// Focus, SkillSpaces, and the permission summary are echoed back so the
	// receipt states what the user actually got rather than only that it
	// worked.
	Focus       FocusResult       `json:"focus"`
	Capacity    ToolboxCapacity   `json:"capacity"`
	Permissions PermissionSummary `json:"permissions"`

	// Previous identifies what was displaced, which is what Undo restores.
	Previous *PriorToolboxAssignment `json:"previous,omitempty"`
	// WorkspaceVersion is the version after the write, so a client can chain a
	// follow-up call without re-reading.
	WorkspaceVersion int64     `json:"workspace_version"`
	AppliedAt        time.Time `json:"applied_at"`
}

// PermissionSummary describes the exposed surface in the terms a user cares
// about: how much, and how dangerous (FR-87).
type PermissionSummary struct {
	Skills             int `json:"skills"`
	Connections        int `json:"connections"`
	Operations         int `json:"operations"`
	ReadOperations     int `json:"read_operations"`
	WriteOperations    int `json:"write_operations"`
	ExternalOperations int `json:"external_operations"`
}

func summarizePermissions(preview ToolboxPreview) PermissionSummary {
	summary := PermissionSummary{
		ReadOperations:     preview.Focus.Inputs.ReadOperations,
		WriteOperations:    preview.Focus.Inputs.WriteOperations,
		ExternalOperations: preview.Focus.Inputs.ExternalOperations,
		Operations:         preview.Focus.Inputs.ExposedOperations,
	}
	for _, skill := range preview.Skills {
		if skill.Available {
			summary.Skills++
		}
	}
	for _, binding := range preview.MCPBindings {
		if binding.Available {
			summary.Connections++
		}
	}
	return summary
}

// ToolboxUseRequest is one switch.
type ToolboxUseRequest struct {
	AgentInstanceID string
	ToolboxID       string
	// ToolboxVersion pins the exact version; 0 means the Toolbox's current one.
	ToolboxVersion int64
	// ExpectedWorkspaceVersion is the version the preview the user consented to
	// was computed against. A mismatch is rejected rather than reconciled
	// (FR-82).
	ExpectedWorkspaceVersion int64
	// AcknowledgedExpansion records that the user completed **Review & Use**.
	// Without it, a switch that would widen permissions is refused — which is
	// what keeps one-click **Use This Toolbox** incapable of granting anything
	// new (FR-78, FR-79).
	AcknowledgedExpansion bool
	Provenance            string
	Actor                 string
}

// ErrToolboxUseNeedsReview means the switch would expand permissions and the
// caller did not come through Review & Use.
var ErrToolboxUseNeedsReview = fmt.Errorf("workspace: this toolbox needs review before it can be used")

// ErrToolboxNotReady means a hard readiness rule failed at save time.
var ErrToolboxNotReady = fmt.Errorf("workspace: this toolbox is not ready to use")

// UseToolbox applies a Toolbox to one stable agent instance as a single
// rollback-safe workspace mutation.
//
// It must be called with a workspace loaded for update; it mutates ws in place
// and the caller saves. Returning an error means the caller discards the
// workspace, which is what makes a failure leave the prior assignment intact
// (FR-85, FR-86).
func UseToolbox(
	ws *Workspace,
	request ToolboxUseRequest,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) (*ToolboxUseResult, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	if err := requireWorkspaceVersion(ws, request.ExpectedWorkspaceVersion); err != nil {
		return nil, err
	}

	instance := findAgentInstanceByID(ws, request.AgentInstanceID)
	if instance == nil {
		return nil, fmt.Errorf("agent instance %s is not attached to this workspace", request.AgentInstanceID)
	}

	definition, exists := ws.GetToolbox(request.ToolboxID)
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, request.ToolboxID)
	}
	version := request.ToolboxVersion
	if version == 0 {
		version = definition.Version
	}
	recipe, err := definition.ResolveVersion(version)
	if err != nil {
		return nil, err
	}

	// Revalidate against the world as it is RIGHT NOW, not as the preview found
	// it. This is the check that makes consent meaningful (FR-83).
	preview := PreviewToolbox(ws, instance, *definition, recipe, learned, capacity, expertMode, thresholds)
	preview.ApplyCurrentAssignmentDiff(ws)

	if preview.Readiness != ReadinessReady {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotReady, firstBlockingMessage(preview.Issues))
	}
	if preview.ExpandsPermissions && !request.AcknowledgedExpansion {
		return nil, ErrToolboxUseNeedsReview
	}

	// From here the write is mechanical. Everything that could refuse has
	// already refused, so the three records below cannot land half-applied.
	assignment, err := ws.SetToolboxAssignment(AgentToolboxAssignment{
		AgentInstanceID: instance.ID,
		ToolboxID:       definition.ID,
		ToolboxVersion:  version,
		Provenance:      firstNonEmpty(request.Provenance, ToolboxProvenanceUser),
		Actor:           request.Actor,
	})
	if err != nil {
		return nil, err
	}

	// The legacy per-instance access entries are kept as a PROJECTION of the
	// assignment. They are no longer consulted by the runtime, but existing
	// read paths and clients still show them, and a stale projection would
	// misinform (FR-36, FR-85).
	if err := projectAssignmentToLegacyAccess(ws, instance.ID, recipe); err != nil {
		return nil, err
	}

	return &ToolboxUseResult{
		AgentInstanceID:  instance.ID,
		AgentName:        instance.Name,
		ToolboxID:        definition.ID,
		ToolboxName:      definition.Name,
		ToolboxVersion:   version,
		Focus:            preview.Focus,
		Capacity:         preview.Capacity,
		Permissions:      summarizePermissions(preview),
		Previous:         assignment.Previous,
		WorkspaceVersion: ws.Version,
		AppliedAt:        assignment.AppliedAt,
	}, nil
}

// projectAssignmentToLegacyAccess rewrites the pre-Toolbox access entries to
// mirror the assignment.
//
// This is deliberately one-directional. The Toolbox is the source of truth; the
// access entries are a view of it. Writing them keeps every older read path
// truthful without giving them any authority (FR-36).
func projectAssignmentToLegacyAccess(ws *Workspace, agentInstanceID string, recipe ToolboxRecipe) error {
	skillBindingIDs := make([]string, 0, len(recipe.Skills))
	for _, ref := range recipe.Skills {
		if NormalizeToolboxSource(ref.Source) == ToolboxSourceWorkspaceProvided && strings.TrimSpace(ref.BindingID) != "" {
			skillBindingIDs = append(skillBindingIDs, ref.BindingID)
		}
	}
	mcpBindingIDs := make([]string, 0, len(recipe.MCPBindings))
	for _, ref := range recipe.MCPBindings {
		if strings.TrimSpace(ref.BindingID) != "" {
			mcpBindingIDs = append(mcpBindingIDs, ref.BindingID)
		}
	}

	if err := ws.SetAgentSkillAccess(AgentSkillAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: skillBindingIDs,
	}); err != nil {
		return err
	}
	return ws.SetAgentMCPAccess(AgentMCPAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: mcpBindingIDs,
	})
}

// UndoToolboxUse restores the assignment that the most recent switch displaced
// (FR-88).
//
// Undo is NOT a bypass. It runs the same preview and the same safety checks as
// a forward switch, because the prior version may have drifted since it was in
// force — a binding it used may have been removed, or the surface it grants may
// now be wider than what is currently active. When that happens the caller must
// come back through **Review & Restore** (FR-89, FR-90).
func UndoToolboxUse(
	ws *Workspace,
	agentInstanceID string,
	expectedWorkspaceVersion int64,
	acknowledgedExpansion bool,
	actor string,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) (*ToolboxUseResult, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	current, exists := ws.GetToolboxAssignment(agentInstanceID)
	if !exists || current.Previous == nil {
		return nil, fmt.Errorf("there is nothing to undo for this agent")
	}

	return UseToolbox(ws, ToolboxUseRequest{
		AgentInstanceID:          agentInstanceID,
		ToolboxID:                current.Previous.ToolboxID,
		ToolboxVersion:           current.Previous.ToolboxVersion,
		ExpectedWorkspaceVersion: expectedWorkspaceVersion,
		AcknowledgedExpansion:    acknowledgedExpansion,
		Provenance:               "undo",
		Actor:                    actor,
	}, learned, capacity, expertMode, thresholds)
}

func findAgentInstanceByID(ws *Workspace, agentInstanceID string) *AgentInstance {
	normalized := strings.TrimSpace(agentInstanceID)
	if normalized == "" {
		return nil
	}
	for _, candidate := range ws.GetAgentInstances() {
		if strings.EqualFold(strings.TrimSpace(candidate.ID), normalized) {
			found := candidate
			return &found
		}
	}
	return nil
}

func firstBlockingMessage(issues []ToolboxIssue) string {
	for _, issue := range issues {
		if issue.Blocking {
			return issue.Message
		}
	}
	return "one or more prerequisites are unresolved"
}
