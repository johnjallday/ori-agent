package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AddAgentRequest represents the request to add an agent to a workspace
type AddAgentRequest struct {
	AgentName string `json:"agent_name"`
}

// AddAgent handles POST /api/workspaces/:id/agents
func (h *HTTPHandler) AddAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		// Extract workspace ID from URL path
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	// Parse request body

	var req AddAgentRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.AgentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		// Get workspace
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		// Add agent using workspace method (creates stable AgentInstance)
		return
	}

	if err := ws.AddAgent(req.AgentName); err != nil {
		if errors.Is(err, ErrAgentAlreadyInWorkspace) {
			_ = orihttp.RespondError(w, http.StatusConflict, err.Error())
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to add agent: %v", err))
		// Save updated workspace
		return
	}

	if err := h.store.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update workspace: %v", err))
		return
	}

	logger.Debug("Added agent to workspace", logger.Fields{"agent": req.AgentName, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Agent added successfully",
		"agent":     req.AgentName,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RemoveAgent handles DELETE /api/workspaces/:id/agents/:agent_name
func (h *HTTPHandler) RemoveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		// Extract workspace ID and agent identifier from URL path
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	agentIdentifier := parts[2]

	// Get workspace
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		// Parse agent identifier to extract name and instance number
		return
	}

	var agentName string
	var instanceNumber int
	if strings.Contains(agentIdentifier, ":") {
		identParts := strings.SplitN(agentIdentifier, ":", 2)
		agentName = identParts[0]
		instanceNumber, err = strconv.Atoi(identParts[1])
		if err != nil {
			orihttp.BadRequest(w, "Invalid instance number format")
			return
		}
	} else {
		agentName = agentIdentifier
		instanceNumber = 0
	}

	// Find the specific agent instance to remove
	var targetInstanceID string

	// If we have AgentInstances, always use them (even if no instance number provided)
	if len(ws.AgentInstances) > 0 {
		if instanceNumber > 0 {
			// Find by name and instance number
			for _, inst := range ws.AgentInstances {
				if inst.Name == agentName && inst.InstanceNumber == instanceNumber {
					targetInstanceID = inst.ID
					break
				}
			}
		} else {
			// No instance number provided - find first matching agent by name
			for _, inst := range ws.AgentInstances {
				if inst.Name == agentName {
					targetInstanceID = inst.ID
					instanceNumber = inst.InstanceNumber // For logging
					break
				}
			}
		}

		if targetInstanceID == "" {
			orihttp.NotFound(w, fmt.Sprintf("Agent instance %s not found", agentName))
			return
		}

		if err := ws.RemoveAgentInstance(targetInstanceID); err != nil {
			if errors.Is(err, ErrWorkspaceEntryAgentRequired) {
				orihttp.BadRequest(w, err.Error())
				return
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to remove agent instance: %v", err))
			return
		}
	} else {

		if err := ws.RemoveAgent(agentName); err != nil {
			if errors.Is(err, ErrWorkspaceEntryAgentRequired) {
				orihttp.BadRequest(w, err.Error())
				return
			}
			orihttp.NotFound(w, fmt.Sprintf("Failed to remove agent: %v", err))
			return
		}
	}

	if err := h.store.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if instanceNumber > 0 {
		logger.Debug("Removed agent instance from workspace", logger.Fields{"instanceNumber": instanceNumber, "workspaceID": workspaceID, "workspace_id": agentName})
	} else {
		logger.Debug("Removed agent from workspace", logger.Fields{"agent": agentName, "workspaceID": workspaceID})
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Agent removed successfully",
		"agent":     agentName,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
