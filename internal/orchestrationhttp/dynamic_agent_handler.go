package orchestrationhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DynamicAgentHandler manages approval flow for dynamic agents.
type DynamicAgentHandler struct {
	workspaceStore workspace.Store
	orchestrator   *orchestration.Orchestrator
	eventBus       *workspace.EventBus
}

// NewDynamicAgentHandler creates a handler for dynamic agent approvals.
func NewDynamicAgentHandler(workspaceStore workspace.Store, orchestrator *orchestration.Orchestrator, eventBus *workspace.EventBus) *DynamicAgentHandler {
	return &DynamicAgentHandler{
		workspaceStore: workspaceStore,
		orchestrator:   orchestrator,
		eventBus:       eventBus,
	}
}

// DynamicAgentApprovalHandler handles POST approvals or denials.
func (h *DynamicAgentHandler) DynamicAgentApprovalHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID := r.URL.Query().Get("workspace_id")
		if workspaceID == "" {
			orihttp.BadRequest(w, "workspace_id is required")
			return
		}
		ws, err := h.workspaceStore.Get(workspaceID)
		if err != nil {
			orihttp.NotFound(w, "workspace not found")
			return
		}
		orihttp.WriteJSON(w, map[string]any{
			"workspace_id":           ws.ID,
			"dynamic_agent_requests": ws.DynamicAgentRequests,
			"pending_plan":           ws.PendingPlan,
		})
		return
	case http.MethodPost:
		// continue below
	default:
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		WorkspaceID string `json:"workspace_id"`
		RequestID   string `json:"request_id"`
		Approve     bool   `json:"approve"`
		ApprovedBy  string `json:"approved_by,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" || req.RequestID == "" {
		orihttp.BadRequest(w, "workspace_id and request_id are required")
		return
	}

	ws, err := h.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		orihttp.NotFound(w, "workspace not found")
		return
	}

	var updatedReq *types.DynamicAgentRequest
	if req.Approve {
		if updatedReq, err = ws.UpdateDynamicAgentRequestStatus(req.RequestID, types.DynamicAgentStatusApproved, req.ApprovedBy); err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
	} else {
		if updatedReq, err = ws.UpdateDynamicAgentRequestStatus(req.RequestID, types.DynamicAgentStatusDenied, req.ApprovedBy); err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		ws.ClearPendingPlan()
	}

	if err := h.workspaceStore.Save(ws); err != nil {
		orihttp.InternalError(w, "failed to save workspace")
		return
	}
	if h.eventBus != nil && updatedReq != nil {
		eventType := workspace.EventDynamicAgentApproved
		if !req.Approve {
			eventType = workspace.EventDynamicAgentDenied
		}
		h.eventBus.Publish(workspace.Event{
			Type:        eventType,
			WorkspaceID: ws.ID,
			Source:      "orchestrationhttp",
			Data: map[string]any{
				"request": updatedReq,
			},
		})
	}

	// Approving a dynamic agent approves THE AGENT. It no longer resumes the
	// pending plan's execution.
	//
	// Those are two decisions, and merging them meant one click created an
	// agent and ran a multi-agent workflow with no durable Plan behind it: no
	// version, no content hash, no record of what was authorized. A user who
	// meant "yes, that agent may exist" got work they never reviewed. Multi-
	// agent execution now goes through a Plan approval like everything else
	// (FR-59, FR-60, FR-149).
	response := map[string]any{
		"success":      true,
		"workspace_id": ws.ID,
		"request_id":   req.RequestID,
		// Retained for clients that read it; always nil now.
		"resume_result": nil,
	}
	if req.Approve && ws.PendingPlan != nil && allApproved(ws, ws.PendingPlan.ID) {
		response["requires_plan"] = true
		response["plan_reason"] = "Every agent this work needs is approved. " +
			"Open a plan to review and approve the work itself."
	}

	orihttp.WriteJSON(w, response)
}

func allApproved(ws *workspace.Workspace, planID string) bool {
	for _, req := range ws.DynamicAgentRequests {
		if planID != "" && req.PlanID != planID {
			continue
		}
		if req.Status != types.DynamicAgentStatusApproved {
			return false
		}
	}
	return true
}
