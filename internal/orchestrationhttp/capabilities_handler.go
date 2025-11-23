package orchestrationhttp

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
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
		http.Error(w, "name parameter required", http.StatusBadRequest)
		return
	}

	agent, ok := ch.agentStore.GetAgent(agentName)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
			http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
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
			log.Printf("❌ Error updating agent capabilities: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated agent %s capabilities and role", agentName)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	// Default priority to 3 (medium) if not specified
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
		log.Printf("❌ Failed to delegate task: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(agentcomm.DelegationResponse{
		TaskID:    task.ID,
		Status:    string(task.Status),
		CreatedAt: task.CreatedAt,
	})
}
