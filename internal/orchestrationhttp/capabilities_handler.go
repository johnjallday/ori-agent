package orchestrationhttp

import (
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CapabilitiesHandler manages agent capabilities and delegation
type CapabilitiesHandler struct {
	agentStore     store.Store
	workspaceStore workspace.Store
	communicator   *agentcomm.Communicator
	eventBus       *workspace.EventBus
}

// NewCapabilitiesHandler creates a new capabilities handler
func NewCapabilitiesHandler(agentStore store.Store, workspaceStore workspace.Store,
	communicator *agentcomm.Communicator, eventBus *workspace.EventBus) *CapabilitiesHandler {
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
		orihttp.BadRequest(w, "name parameter required")
		return
	}

	agent, ok := ch.agentStore.GetAgent(agentName)
	if !ok {
		orihttp.NotFound(w, "agent not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		orihttp.WriteJSON(w, map[string]any{
			"agent":        agentName,
			"role":         agent.Role,
			"capabilities": agent.Capabilities,
		})

	case http.MethodPut:
		var req struct {
			Role         string   `json:"role"`
			Capabilities []string `json:"capabilities"`
		}

		if !orihttp.ParseJSONBody(w, r, &req) {
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
			orihttp.InternalError(w, err.Error())
			return
		}

		logger.Info("Updated agent capabilities and role", logger.Fields{"agent": agentName})

		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]any{
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
		WorkspaceID string         `json:"workspace_id"`
		From        string         `json:"from"`
		To          string         `json:"to"`
		Description string         `json:"description"`
		Priority    int            `json:"priority"`
		Context     map[string]any `json:"context"`
		Timeout     int            `json:"timeout"` // timeout in seconds
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate required fields

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if req.From == "" {
		orihttp.BadRequest(w, "from is required")
		return
	}
	if req.To == "" {
		orihttp.BadRequest(w, "to is required")
		return
	}
	if req.Description == "" {
		orihttp.BadRequest(w, "description is required")
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
		logger.Error("Failed to delegate task", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, agentcomm.DelegationResponse{
		TaskID:    task.ID,
		Status:    string(task.Status),
		CreatedAt: task.CreatedAt,
	})
}
