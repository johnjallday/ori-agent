package orchestrationhttp

import (
	"encoding/json"

	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// CapabilitiesHandler manages agent capabilities and delegation
type CapabilitiesHandler struct {
	agentStore     store.Store
	workspaceStore agentstudio.Store
	communicator   *agentcomm.Communicator
	eventBus       *agentstudio.EventBus
}

// NewCapabilitiesHandler creates a new capabilities handler
func NewCapabilitiesHandler(agentStore store.Store, workspaceStore agentstudio.Store,
	communicator *agentcomm.Communicator, eventBus *agentstudio.EventBus) *CapabilitiesHandler {
	return &CapabilitiesHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		communicator:   communicator,
		eventBus:       eventBus,
	}
}

// AgentCapabilitiesHandler handles agent capability management
// GET: Get agent capabilities
// PUT: Update agent capabilities
func (ch *CapabilitiesHandler) AgentCapabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentName := r.URL.Query().Get("name")
	if agentName == "" {
		if err := orihttp.RespondBadRequest(w, "name parameter required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	agent, ok := ch.agentStore.GetAgent(agentName)
	if !ok {
		if err := orihttp.RespondNotFound(w, "agent not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		orihttp.WriteJSON(w, map[string]interface{}{
			"agent":        agentName,
			"role":         agent.Role,
			"capabilities": agent.Capabilities,
		})

	case http.MethodPut:
		var req struct {
			Role         string   `json:"role"`
			Capabilities []string `json:"capabilities"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if err := orihttp.RespondBadRequest(w, "Invalid request body: "+err.Error()); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		// Update agent role and capabilities
		if req.Role != "" {
			agent.Role = types.AgentRole(req.Role)
		}
		if req.Capabilities != nil {
			agent.Capabilities = req.Capabilities
		}

		if err := ch.agentStore.SetAgent(agentName, agent); err != nil {
			logger.Error("Error updating agent capabilities", logger.Fields{"error": err})
			if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		logger.Info("Updated agent capabilities and role", logger.Fields{"agent": agentName})

		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"agent":   agentName,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// DelegateHandler handles task delegation between agents
// POST: Delegate a task to another agent
func (ch *CapabilitiesHandler) DelegateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkspaceID string                 `json:"studio_id"`
		From        string                 `json:"from"`
		To          string                 `json:"to"`
		Description string                 `json:"description"`
		Priority    int                    `json:"priority"`
		Context     map[string]interface{} `json:"context"`
		Timeout     int                    `json:"timeout"` // timeout in seconds
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body: "+err.Error()); err != nil {
			logger.

				// Validate required fields
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.WorkspaceID == "" {
		if err := orihttp.RespondBadRequest(w, "workspace_id is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	if req.From == "" {
		if err := orihttp.RespondBadRequest(w, "from is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	if req.To == "" {
		if err := orihttp.RespondBadRequest(w, "to is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	if req.Description == "" {
		if err := orihttp.RespondBadRequest(w, "description is required"); err != nil {
			logger.

				// Default priority to 3 (medium) if not specified
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Priority == 0 {
		req.Priority = 3
	}

	// Convert timeout from seconds to duration
	timeout := time.Duration(req.Timeout) * time.Second
	if req.Timeout == 0 {
		timeout = 5 * time.Minute // Default timeout
	}

	// Delegate task
	task, err := ch.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     req.Context,
		Timeout:     timeout,
	})

	if err != nil {
		logger.Error("Failed to delegate task", logger.Fields{"task_id": err})
		if err := orihttp.RespondBadRequest(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, agentcomm.DelegationResponse{
		TaskID:    task.ID,
		Status:    string(task.Status),
		CreatedAt: task.CreatedAt,
	})
}
