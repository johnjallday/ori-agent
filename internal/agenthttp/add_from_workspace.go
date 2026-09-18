package agenthttp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// workspaceAgentAdder is the composite store's "Add to my agents".
type workspaceAgentAdder interface {
	AddWorkspaceAgent(workspaceID, name string) (string, error)
}

// HandleAddFromWorkspace serves POST /api/agents/add-from-workspace with
// {workspace_id, name}. It copies an agent that only that workspace holds into
// the user's own agents. This is the only way a workspace's copy becomes one of
// the user's agents; Ori never does it on its own.
func (h *Handler) HandleAddFromWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.Name = strings.TrimSpace(req.Name)
	if req.WorkspaceID == "" || req.Name == "" {
		orihttp.BadRequest(w, "workspace_id and name are required")
		return
	}
	adder, ok := h.State.(workspaceAgentAdder)
	if !ok {
		orihttp.ServiceUnavailable(w, "Adding a workspace's agent is not available with this agent store.")
		return
	}

	name, err := adder.AddWorkspaceAgent(req.WorkspaceID, req.Name)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrWorkspaceAgentNotFound):
		orihttp.NotFound(w, "That workspace has no agent by this name.")
		return
	case errors.Is(err, store.ErrAgentAlreadyExists):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"code":    "agent_exists",
			"error":   fmt.Sprintf("You already have an agent named %s.", req.Name),
			"message": fmt.Sprintf("You already have an agent named %s.", req.Name),
		})
		return
	default:
		logger.Error("Failed to add a workspace's agent", logger.Fields{"workspace_id": req.WorkspaceID, "agent": req.Name, "error": err})
		WriteAgentStoreError(w, "Failed to add the agent", err)
		return
	}

	if h.ActivityLogger != nil {
		if logErr := h.ActivityLogger.LogActivity(name, types.ActivityEventCreated, map[string]any{"from_workspace": req.WorkspaceID}, ""); logErr != nil {
			logger.Error("Failed to log activity", logger.Fields{"error": logErr})
		}
	}
	orihttp.Created(w, map[string]any{
		"success": true,
		"name":    name,
		"origin":  originFor(h.State, name),
	})
}
