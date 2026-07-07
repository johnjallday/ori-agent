package sessionhttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// maxCustomInstructionsLen bounds a per-instance refinement string (PRD FR17).
const maxCustomInstructionsLen = 2000

// UpdateWorkspaceAgentInstanceSettings handles
// PATCH /api/workspaces/{workspaceID}/agents/{name}/instance-settings.
//
// It updates the per-instance role, description, and custom_instructions on the
// workspace's AgentInstance(s) for the named agent — the workspace owner's
// refinement of a shared definition — WITHOUT touching the global agent
// definition or the workspace-local agent snapshot (PRD FR16/FR17/FR18). Fields
// are partial: only keys present in the body are changed.
func (h *Handler) UpdateWorkspaceAgentInstanceSettings(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 ServeMux path values are already percent-decoded.
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	agentName := strings.TrimSpace(r.PathValue("name"))
	if workspaceID == "" || agentName == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id and agent name are required")
		return
	}

	var req struct {
		Role               *string `json:"role,omitempty"`
		Description        *string `json:"description,omitempty"`
		CustomInstructions *string `json:"custom_instructions,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Trim outer whitespace (which also drops leading/trailing newlines) while
	// preserving intentional internal newlines, and cap the length (PRD FR17).
	var trimmedCustom string
	if req.CustomInstructions != nil {
		trimmedCustom = strings.TrimSpace(*req.CustomInstructions)
		if len(trimmedCustom) > maxCustomInstructionsLen {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("custom_instructions exceeds %d characters", maxCustomInstructionsLen))
			return
		}
	}

	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	matched := 0
	for i := range workspace.AgentInstances {
		if !strings.EqualFold(strings.TrimSpace(workspace.AgentInstances[i].Name), agentName) {
			continue
		}
		matched++
		if req.Role != nil {
			workspace.AgentInstances[i].Role = strings.TrimSpace(*req.Role)
		}
		if req.Description != nil {
			workspace.AgentInstances[i].Description = strings.TrimSpace(*req.Description)
		}
		if req.CustomInstructions != nil {
			workspace.AgentInstances[i].CustomInstructions = trimmedCustom
		}
	}
	if matched == 0 {
		_ = orihttp.RespondNotFound(w, fmt.Sprintf("Agent %q is not attached to this workspace", agentName))
		return
	}

	workspace.UpdatedAt = time.Now()
	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace agent settings", logger.Fields{"id": workspaceID, "agent": agentName, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update agent settings")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after updating agent settings", logger.Fields{"id": workspaceID, "error": err})
	}

	logger.Info("Updated workspace agent instance settings", logger.Fields{
		"workspace_id": workspaceID, "agent": agentName, "instances": matched,
	})
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":   true,
		"agent":     agentName,
		"instances": matched,
		"workspace": workspace,
	})
}
