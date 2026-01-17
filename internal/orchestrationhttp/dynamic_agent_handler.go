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
			Data: map[string]interface{}{
				"request": updatedReq,
			},
		})
	}

	resumeResult := interface{}(nil)
	if req.Approve && ws.PendingPlan != nil && h.orchestrator != nil {
		if allApproved(ws, ws.PendingPlan.ID) {
			result, err := h.orchestrator.ResumePendingPlan(r.Context(), ws.ID)
			if err == nil {
				resumeResult = result
			}
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"success":       true,
		"workspace_id":  ws.ID,
		"request_id":    req.RequestID,
		"resume_result": resumeResult,
	})
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
