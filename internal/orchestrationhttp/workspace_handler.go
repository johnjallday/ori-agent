package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/store"
)

// WorkspaceHandler manages workspace-related operations
type WorkspaceHandler struct {
	agentStore     store.Store
	workspaceStore agentstudio.Store
	eventBus       *agentstudio.EventBus
}

// NewWorkspaceHandler creates a new workspace handler
func NewWorkspaceHandler(agentStore store.Store, workspaceStore agentstudio.Store, eventBus *agentstudio.EventBus) *WorkspaceHandler {
	return &WorkspaceHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		eventBus:       eventBus,
	}
}

// WorkspaceHandler handles workspace CRUD operations
// GET: List all workspaces or get workspace by ID
// POST: Create new workspace
// DELETE: Delete workspace
func (wh *WorkspaceHandler) WorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		wh.handleGetWorkspace(w, r)
	case http.MethodPost:
		wh.handleCreateWorkspace(w, r)
	case http.MethodDelete:
		wh.handleDeleteWorkspace(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetWorkspace retrieves workspace(s)
func (wh *WorkspaceHandler) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	// Check if a specific workspace ID is requested
	wsID := r.URL.Query().Get("id")
	activeOnly := r.URL.Query().Get("active") == "true"

	if wsID != "" {
		// Get specific workspace
		ws, err := wh.workspaceStore.Get(wsID)
		if err != nil {
			log.Printf("❌ Error getting workspace %s: %v", wsID, err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		_ = json.NewEncoder(w).Encode(ws)
		return
	}

	// List workspaces with optional filters
	if activeOnly {
		// Get only active workspaces
		workspaces, err := wh.workspaceStore.ListActive()
		if err != nil {
			log.Printf("❌ Error listing active workspaces: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workspaces": workspaces,
			"count":      len(workspaces),
		})
		return
	}

	// List all workspaces
	ids, err := wh.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load summaries for all workspaces
	summaries := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		ws, err := wh.workspaceStore.Get(id)
		if err != nil {
			continue // Skip workspaces that fail to load
		}
		summaries = append(summaries, ws.GetSummary())
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"workspaces": summaries,
		"count":      len(summaries),
	})
}

// handleCreateWorkspace creates a new workspace
func (wh *WorkspaceHandler) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Agents      []string               `json:"participating_agents"`
		InitialData map[string]interface{} `json:"initial_context"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ Error decoding workspace creation request: %v", err)
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Verify all participating agents exist
	for _, agentName := range req.Agents {
		_, ok := wh.agentStore.GetAgent(agentName)
		if !ok {
			http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
			return
		}
	}

	// Create workspace
	ws := agentstudio.NewWorkspace(agentstudio.CreateWorkspaceParams{
		Name:        req.Name,
		Description: req.Description,
		Agents:      req.Agents,
		InitialData: req.InitialData,
	})

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Created workspace: %s (ID: %s)", req.Name, ws.ID)

	// Publish workspace created event
	if wh.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceCreated,
			ws.ID,
			"api",
			map[string]interface{}{
				"name":        req.Name,
				"description": req.Description,
				"agents":      req.Agents,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"studio_id":  ws.ID,
		"status":     ws.Status,
		"created_at": ws.CreatedAt,
	})
}

// handleDeleteWorkspace deletes a workspace
func (wh *WorkspaceHandler) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	if err := wh.workspaceStore.Delete(wsID); err != nil {
		log.Printf("❌ Error deleting workspace %s: %v", wsID, err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	log.Printf("✅ Deleted workspace: %s", wsID)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Workspace deleted successfully",
	})
}

// WorkspaceAgentsHandler handles adding/removing agents from workspace
// POST: Add agent to workspace
// DELETE: Remove agent from workspace
func (wh *WorkspaceHandler) WorkspaceAgentsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodPost:
		wh.handleAddAgentToWorkspace(w, r)
	case http.MethodDelete:
		wh.handleRemoveAgentFromWorkspace(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleAddAgentToWorkspace adds an agent to a workspace
func (wh *WorkspaceHandler) handleAddAgentToWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"studio_id"`
		AgentName   string `json:"agent_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.AgentName == "" {
		http.Error(w, "agent_name is required", http.StatusBadRequest)
		return
	}

	// Verify agent exists
	_, ok := wh.agentStore.GetAgent(req.AgentName)
	if !ok {
		http.Error(w, "agent not found: "+req.AgentName, http.StatusNotFound)
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Add agent
	if err := ws.AddAgent(req.AgentName); err != nil {
		log.Printf("❌ Error adding agent to workspace: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Added agent %s to workspace %s", req.AgentName, req.WorkspaceID)

	// Publish event
	if wh.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceUpdated,
			req.WorkspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_added",
				"agent":  req.AgentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"agents":  ws.Agents,
	})
}

// handleRemoveAgentFromWorkspace removes an agent from a workspace
func (wh *WorkspaceHandler) handleRemoveAgentFromWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent_name")

	if workspaceID == "" {
		http.Error(w, "workspace_id parameter required", http.StatusBadRequest)
		return
	}
	if agentName == "" {
		http.Error(w, "agent_name parameter required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", workspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Remove agent
	if err := ws.RemoveAgent(agentName); err != nil {
		log.Printf("❌ Error removing agent from workspace: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Removed agent %s from workspace %s", agentName, workspaceID)

	// Publish event
	if wh.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceUpdated,
			workspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_removed",
				"agent":  agentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Agent removed successfully",
		"agent":   agentName,
		"agents":  ws.Agents,
	})
}

// SaveLayoutHandler saves the canvas layout for a workspace
// PUT: Save workspace layout (task positions, agent positions, zoom, pan)
func (wh *WorkspaceHandler) SaveLayoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkspaceID         string                                 `json:"workspace_id"`
		TaskPositions       map[string]agentstudio.Position        `json:"task_positions"`
		AgentPositions      map[string]agentstudio.Position        `json:"agent_positions"`
		CombinerNodes       []agentstudio.CombinerNodeLayout       `json:"combiner_nodes"`
		WorkflowConnections []agentstudio.WorkflowConnectionLayout `json:"workflow_connections"`
		Scale               float64                                `json:"scale"`
		OffsetX             float64                                `json:"offset_x"`
		OffsetY             float64                                `json:"offset_y"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get workspace: %v", err), http.StatusNotFound)
		return
	}

	// Update layout
	if ws.Layout == nil {
		ws.Layout = &agentstudio.CanvasLayout{}
	}

	ws.Layout.TaskPositions = req.TaskPositions
	ws.Layout.AgentPositions = req.AgentPositions
	ws.Layout.CombinerNodes = req.CombinerNodes
	ws.Layout.WorkflowConnections = req.WorkflowConnections
	ws.Layout.Scale = req.Scale
	ws.Layout.OffsetX = req.OffsetX
	ws.Layout.OffsetY = req.OffsetY

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save workspace: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("💾 Saved layout for workspace %s (tasks: %d, agents: %d, combiners: %d, conns: %d, scale: %.2f)",
		req.WorkspaceID, len(req.TaskPositions), len(req.AgentPositions), len(req.CombinerNodes), len(req.WorkflowConnections), req.Scale)

	// Broadcast workspace update event to notify all connected clients
	wh.eventBus.Publish(agentstudio.Event{
		WorkspaceID: req.WorkspaceID,
		Type:        agentstudio.EventWorkspaceUpdated,
		Timestamp:   time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Layout saved successfully",
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}
