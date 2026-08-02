package workspace

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Preview, use, and undo endpoints (PRD FR-74–FR-91, tasks 3.7, 3.13, 3.16).
//
// The split is deliberate and load-bearing: PREVIEW is a GET that cannot write,
// and USE is a POST that revalidates everything the preview claimed. A single
// endpoint that did both would make "show me what would happen" and "do it"
// the same request, which is exactly the shape that makes accidental
// permission grants possible.

// resolveToolboxContext gathers the per-instance inputs every one of these
// endpoints needs: the instance, its capacity, and its learned skills.
func (h *HTTPHandler) resolveToolboxContext(ws *Workspace, agentInstanceID string) (*AgentInstance, []ResolvedSkill, int, bool, error) {
	instance := findAgentInstanceByID(ws, agentInstanceID)
	if instance == nil {
		return nil, nil, 0, false, fmt.Errorf("agent instance %s is not attached to this workspace", agentInstanceID)
	}

	var learned []ResolvedSkill
	if h.toolboxSkills != nil {
		if resolved, err := h.toolboxSkills.ListEnabledAgentSkills(instance.Name); err == nil {
			learned = resolved
		}
	}

	capacity, expertMode := 0, false
	if h.toolboxCapacity != nil {
		if resolvedCapacity, resolvedExpert, ok := h.toolboxCapacity.ResolveAgentCapacity(instance.Name); ok {
			capacity, expertMode = resolvedCapacity, resolvedExpert
		}
	}
	return instance, learned, capacity, expertMode, nil
}

// PreviewToolboxHandler handles GET
// /api/workspaces/{workspaceID}/agent-toolboxes/{agentInstanceID}/preview
//
// Query: toolbox_id (required), version (optional).
//
// Read-only by construction — it never calls Update or Save (FR-74, FR-76).
// Note that it also does NOT run the migration ensure the other read endpoints
// do: a preview must not write, and an instance with no assignment previews
// perfectly well as "everything here is new".
func (h *HTTPHandler) PreviewToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}
	toolboxID := strings.TrimSpace(r.URL.Query().Get("toolbox_id"))
	if toolboxID == "" {
		orihttp.BadRequest(w, "toolbox_id is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	instance, learned, capacity, expertMode, err := h.resolveToolboxContext(ws, agentInstanceID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	definition, exists := ws.GetToolbox(toolboxID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Toolbox %s not found", toolboxID))
		return
	}
	version := definition.Version
	if raw := strings.TrimSpace(r.URL.Query().Get("version")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			orihttp.BadRequest(w, fmt.Sprintf("invalid version %q", raw))
			return
		}
		version = parsed
	}
	recipe, err := definition.ResolveVersion(version)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	preview := PreviewToolbox(ws, instance, *definition, recipe, learned, capacity, expertMode, DefaultFocusThresholds())
	preview.ApplyCurrentAssignmentDiff(ws)

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"preview": preview,
		// The label the UI should show. Naming it here keeps the Use/Review
		// decision server-owned rather than re-derived by each surface (FR-78).
		"action":            useActionLabel(preview),
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

// useActionLabel names the control a user should be offered.
func useActionLabel(preview ToolboxPreview) string {
	if preview.CanUseDirectly {
		return "Use This Toolbox"
	}
	return "Review & Use"
}

// UseToolboxHandler handles POST
// /api/workspaces/{workspaceID}/agent-toolboxes/{agentInstanceID}/use
//
// One request, one atomic mutation, one committed event (FR-81, FR-91).
func (h *HTTPHandler) UseToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	var req struct {
		ToolboxID      string `json:"toolbox_id"`
		ToolboxVersion int64  `json:"toolbox_version,omitempty"`
		// ExpectedWorkspaceVersion carries the version the user's preview was
		// computed against (FR-82).
		ExpectedWorkspaceVersion int64 `json:"expected_workspace_version,omitempty"`
		// AcknowledgedExpansion records completed Review & Use (FR-79).
		AcknowledgedExpansion bool `json:"acknowledged_expansion,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ToolboxID) == "" {
		orihttp.BadRequest(w, "toolbox_id is required")
		return
	}

	var result *ToolboxUseResult
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		_, learned, capacity, expertMode, contextErr := h.resolveToolboxContext(ws, agentInstanceID)
		if contextErr != nil {
			return contextErr
		}
		used, useErr := UseToolbox(ws, ToolboxUseRequest{
			AgentInstanceID:          agentInstanceID,
			ToolboxID:                req.ToolboxID,
			ToolboxVersion:           req.ToolboxVersion,
			ExpectedWorkspaceVersion: req.ExpectedWorkspaceVersion,
			AcknowledgedExpansion:    req.AcknowledgedExpansion,
			Provenance:               ToolboxProvenanceUser,
			Actor:                    toolboxActor(r),
		}, learned, capacity, expertMode, DefaultFocusThresholds())
		if useErr != nil {
			return useErr
		}
		result = used
		return nil
	})
	if err != nil {
		h.writeUseError(w, workspaceID, agentInstanceID, err)
		return
	}

	// One event, published only after the complete assignment succeeded, so
	// Map and Details converge on the same state (FR-91, §9.5).
	h.publishToolboxEvent(workspaceID, "toolbox_used", map[string]any{
		"agent_instance_id": result.AgentInstanceID,
		"toolbox_id":        result.ToolboxID,
		"toolbox_version":   result.ToolboxVersion,
		"receipt":           result,
	})

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   fmt.Sprintf("%s is now using %s v%d.", result.AgentName, result.ToolboxName, result.ToolboxVersion),
		"receipt":   result,
		"workspace": workspaceID,
	})
}

// writeUseError maps a failed switch onto a status and, crucially, tells the
// user what did NOT change (FR-86, FR-167).
func (h *HTTPHandler) writeUseError(w http.ResponseWriter, workspaceID, agentInstanceID string, err error) {
	unchanged := "Nothing changed — this agent is still using its previous toolbox."

	switch {
	case errors.Is(err, errStaleWorkspaceVersion):
		// A stale write gets enough information to refresh and preview again
		// rather than a bare conflict (FR-82).
		payload := map[string]any{
			"message":   err.Error() + " " + unchanged,
			"unchanged": true,
			"workspace": workspaceID,
		}
		if ws, getErr := h.store.Get(workspaceID); getErr == nil {
			payload["workspace_version"] = ws.Version
			if view, ok := buildAgentToolboxView(ws, agentInstanceID); ok {
				payload["current"] = view
			}
		}
		writeToolboxJSON(w, http.StatusConflict, payload)
	case errors.Is(err, ErrToolboxUseNeedsReview):
		writeToolboxJSON(w, http.StatusConflict, map[string]any{
			"message":       err.Error() + " " + unchanged,
			"needs_review":  true,
			"unchanged":     true,
			"review_action": "Review & Use",
			"workspace":     workspaceID,
		})
	case errors.Is(err, ErrToolboxNotReady):
		writeToolboxJSON(w, http.StatusConflict, map[string]any{
			"message":   err.Error() + " " + unchanged,
			"unchanged": true,
			"workspace": workspaceID,
		})
	case errors.Is(err, ErrToolboxNotFound), errors.Is(err, ErrToolboxVersionNotFound):
		orihttp.NotFound(w, err.Error())
	default:
		orihttp.BadRequest(w, err.Error()+" "+unchanged)
	}
}

// PreviewUndoToolboxHandler handles GET
// /api/workspaces/{workspaceID}/agent-toolboxes/{agentInstanceID}/undo
//
// Undo previews the INVERSE change and revalidates it, because the version it
// would restore may have drifted since it was in force (FR-89, FR-90).
func (h *HTTPHandler) PreviewUndoToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	assignment, exists := ws.GetToolboxAssignment(agentInstanceID)
	if !exists || assignment.Previous == nil {
		writeToolboxJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"message":   "There is nothing to undo for this agent.",
			"workspace": workspaceID,
		})
		return
	}

	instance, learned, capacity, expertMode, err := h.resolveToolboxContext(ws, agentInstanceID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	definition, exists := ws.GetToolbox(assignment.Previous.ToolboxID)
	if !exists {
		// The prior Toolbox is gone, so there is no safe inverse to offer.
		// Saying so beats offering an Undo that would fail on click.
		writeToolboxJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"message":   "The toolbox this agent used before is no longer available.",
			"workspace": workspaceID,
		})
		return
	}
	recipe, err := definition.ResolveVersion(assignment.Previous.ToolboxVersion)
	if err != nil {
		writeToolboxJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"message":   "The exact version this agent used before is no longer available.",
			"workspace": workspaceID,
		})
		return
	}

	preview := PreviewToolbox(ws, instance, *definition, recipe, learned, capacity, expertMode, DefaultFocusThresholds())
	preview.ApplyCurrentAssignmentDiff(ws)

	// Restoring is still a switch, so it gets the same gate. A prior version
	// that has drifted into needing review does not get to skip it (FR-90).
	action := "Undo"
	if !preview.CanUseDirectly {
		action = "Review & Restore"
	}

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"available":         true,
		"preview":           preview,
		"action":            action,
		"previous":          assignment.Previous,
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

// UndoToolboxHandler handles POST
// /api/workspaces/{workspaceID}/agent-toolboxes/{agentInstanceID}/undo
func (h *HTTPHandler) UndoToolboxHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	var req struct {
		ExpectedWorkspaceVersion int64 `json:"expected_workspace_version,omitempty"`
		AcknowledgedExpansion    bool  `json:"acknowledged_expansion,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var result *ToolboxUseResult
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		_, learned, capacity, expertMode, contextErr := h.resolveToolboxContext(ws, agentInstanceID)
		if contextErr != nil {
			return contextErr
		}
		restored, undoErr := UndoToolboxUse(ws, agentInstanceID, req.ExpectedWorkspaceVersion,
			req.AcknowledgedExpansion, toolboxActor(r), learned, capacity, expertMode, DefaultFocusThresholds())
		if undoErr != nil {
			return undoErr
		}
		result = restored
		return nil
	})
	if err != nil {
		h.writeUseError(w, workspaceID, agentInstanceID, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "toolbox_used", map[string]any{
		"agent_instance_id": result.AgentInstanceID,
		"toolbox_id":        result.ToolboxID,
		"toolbox_version":   result.ToolboxVersion,
		"undo":              true,
		"receipt":           result,
	})

	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   fmt.Sprintf("Restored %s v%d for %s.", result.ToolboxName, result.ToolboxVersion, result.AgentName),
		"receipt":   result,
		"workspace": workspaceID,
	})
}
