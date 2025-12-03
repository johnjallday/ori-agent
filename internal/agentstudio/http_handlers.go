package agentstudio

import (
	"encoding/json"
	"fmt"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// HTTPHandler handles HTTP requests for Agent Studio
type HTTPHandler struct {
	store        Store
	orchestrator *Orchestrator
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(store Store, orchestrator *Orchestrator) *HTTPHandler {
	return &HTTPHandler{
		store:        store,
		orchestrator: orchestrator,
	}
}

// CreateStudioRequest represents the request to create a new studio
type CreateStudioRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Agents      []string `json:"agents"`
}

// CreateStudio handles POST /api/studios
func (h *HTTPHandler) CreateStudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateStudioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Name == "" {
		http.Error(w, "Studio name is required", http.StatusBadRequest)
		return
	}
	if len(req.Agents) == 0 {
		http.Error(w, "At least one agent is required", http.StatusBadRequest)
		return
	}

	// Create studio
	studio := &Workspace{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		Agents:      req.Agents,
		SharedData:  make(map[string]interface{}),
		Messages:    make([]AgentMessage, 0),
		Tasks:       make([]Task, 0),
		Status:      StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		logger.Error("Failed to save studio", logger.Fields{"workspace_id": err})
		http.Error(w, "Failed to create studio", http.StatusInternalServerError)
		return
	}

	logger.Info("Created studio: (ID: ) with agents", logger.Fields{"workspace_id": studio.Name, "id": studio.ID, "agents": studio.Agents})

	// Return created studio
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      studio.ID,
		"name":    studio.Name,
		"agents":  studio.Agents,
		"status":  studio.Status,
		"message": "Studio created successfully",
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetStudio handles GET /api/studios/:id
func (h *HTTPHandler) GetStudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	studioID := strings.TrimSuffix(path, "/")

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Get agent statistics
	agentStats := studio.GetAgentStats()

	// Get workspace progress
	workspaceProgress := studio.GetWorkspaceProgress()

	// Return studio details
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                 studio.ID,
		"name":               studio.Name,
		"description":        studio.Description,
		"agents":             studio.Agents,
		"agent_instances":    studio.AgentInstances, // NEW: Stable agent instances
		"agent_stats":        agentStats,
		"workspace_progress": workspaceProgress,
		"status":             studio.Status,
		"tasks":              studio.Tasks,
		"messages":           studio.Messages,
		"shared_data":        studio.SharedData,
		"layout":             studio.Layout,
		"created_at":         studio.CreatedAt,
		"updated_at":         studio.UpdatedAt,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// ListStudios handles GET /api/studios
func (h *HTTPHandler) ListStudios(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all studio IDs
	ids, err := h.store.List()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list studios: %v", err), http.StatusInternalServerError)
		return
	}

	// Get studio details
	studios := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		studio, err := h.store.Get(id)
		if err != nil {
			logger.Error("Failed to get studio", logger.Fields{"workspace_id": id, "err": err})
			continue
		}

		studios = append(studios, map[string]interface{}{
			"id":          studio.ID,
			"name":        studio.Name,
			"description": studio.Description,
			"agents":      studio.Agents,
			"status":      studio.Status,
			"created_at":  studio.CreatedAt,
			"task_count":  len(studio.Tasks),
		})
	}

	// Return studios
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"studios": studios,
		"count":   len(studios),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetStudioEvents handles GET /api/studios/:id/events (Server-Sent Events)
func (h *HTTPHandler) GetStudioEvents(w http.ResponseWriter, r *http.Request) {
	// Extract studio ID
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	// Verify studio exists
	if _, err := h.store.Get(studioID); err != nil {
		http.Error(w, "Studio not found", http.StatusNotFound)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create event channel
	events := make(chan Event, 10)

	// Subscribe to events
	// TODO: Implement event subscription filtering by studio ID
	// For now, just send a test event
	go func() {
		time.Sleep(1 * time.Second)
		events <- Event{
			ID:          uuid.New().String(),
			Type:        EventType("info"),
			WorkspaceID: studioID,
			Timestamp:   time.Now(),
			Source:      "system",
			Data: map[string]interface{}{
				"message": "Connected to event stream",
			},
			Metadata: make(map[string]string),
		}
	}()

	// Stream events
	for {
		select {
		case event := <-events:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()

		case <-r.Context().Done():
			// Client disconnected
			return
		}
	}
}

// AddAgentRequest represents the request to add an agent to a workspace
type AddAgentRequest struct {
	AgentName string `json:"agent_name"`
}

// AddAgent handles POST /api/studios/:id/agents
func (h *HTTPHandler) AddAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	// Parse request body
	var req AddAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.AgentName == "" {
		http.Error(w, "Agent name is required", http.StatusBadRequest)
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Add agent using workspace method (creates stable AgentInstance)
	if err := studio.AddAgent(req.AgentName); err != nil {
		http.Error(w, fmt.Sprintf("Failed to add agent: %v", err), http.StatusInternalServerError)
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update studio: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Debug("Added agent to studio", logger.Fields{"agent": req.AgentName, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// RemoveAgent handles DELETE /api/studios/:id/agents/:agent_name
func (h *HTTPHandler) RemoveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID and agent identifier from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	agentIdentifier := parts[2] // Format: "name" or "name:instanceNumber"

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Parse agent identifier to extract name and instance number
	var agentName string
	var instanceNumber int
	if strings.Contains(agentIdentifier, ":") {
		identParts := strings.SplitN(agentIdentifier, ":", 2)
		agentName = identParts[0]
		instanceNumber, err = strconv.Atoi(identParts[1])
		if err != nil {
			http.Error(w, "Invalid instance number format", http.StatusBadRequest)
			return
		}
	} else {
		agentName = agentIdentifier
		instanceNumber = 0 // Legacy: remove first occurrence
	}

	// Find the specific agent instance to remove
	var targetInstanceID string
	if instanceNumber > 0 {
		// NEW: Find by name and instance number using stable AgentInstances
		for _, inst := range studio.AgentInstances {
			if inst.Name == agentName && inst.InstanceNumber == instanceNumber {
				targetInstanceID = inst.ID
				break
			}
		}
		if targetInstanceID == "" {
			http.Error(w, fmt.Sprintf("Agent instance %s #%d not found", agentName, instanceNumber), http.StatusNotFound)
			return
		}

		// Remove using new method (maintains stable node IDs)
		if err := studio.RemoveAgentInstance(targetInstanceID); err != nil {
			http.Error(w, fmt.Sprintf("Failed to remove agent instance: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		// LEGACY: Remove by name (removes first occurrence)
		if err := studio.RemoveAgent(agentName); err != nil {
			http.Error(w, fmt.Sprintf("Failed to remove agent: %v", err), http.StatusNotFound)
			return
		}
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
		return
	}

	if instanceNumber > 0 {
		logger.Debug("Removed agent instance # from studio", logger.Fields{"instanceNumber": instanceNumber, "studioID": studioID, "workspace_id": agentName})
	} else {
		logger.Debug("Removed agent from studio", logger.Fields{"agent": agentName, "studioID": studioID})
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent removed successfully",
		"agent":   agentName,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// CreateTaskRequest represents the request to create a task
type CreateTaskRequest struct {
	Description string `json:"description"`
	From        string `json:"from"`
	To          string `json:"to"`
	Priority    int    `json:"priority"`
}

// CreateTask handles POST /api/studios/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	// Parse request body
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Description == "" {
		http.Error(w, "Task description is required", http.StatusBadRequest)
		return
	}
	// Note: From and To agents are optional - tasks can be created without connections

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	logger.Debug("[DEBUG] CreateTask - Studio: , Agents", logger.Fields{"agent": studioID, "agents": studio.Agents})
	logger.Debug("[DEBUG] CreateTask - Request: From=, To=", logger.Fields{"task_id": req.From, "to": req.To})

	// Create task
	task := Task{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     make(map[string]interface{}),
		Status:      TaskStatusPending,
		CreatedAt:   time.Now(),
	}

	// Add task to studio
	if err := studio.AddTask(task); err != nil {
		logger.Error("[DEBUG] CreateTask - AddTask failed", logger.Fields{"task_id": err})
		http.Error(w, fmt.Sprintf("Failed to add task: %v", err), http.StatusInternalServerError)
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Info("Created task in studio", logger.Fields{"description": req.Description, "task_id": task.ID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task created successfully",
		"task_id": task.ID,
		"task":    task,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UpdateTask handles PATCH /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Parse request body
	var req struct {
		Description    string   `json:"description"`
		To             string   `json:"to"`
		From           string   `json:"from"`
		InputTaskIDs   []string `json:"input_task_ids"`
		AssignedNodeID string   `json:"assigned_node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find and update task
	found := false
	for i, task := range studio.Tasks {
		if task.ID == taskID {
			// Update only provided fields
			if req.Description != "" {
				studio.Tasks[i].Description = req.Description
			}
			// Allow updating to/from even if empty (for unassigning)
			studio.Tasks[i].To = req.To
			studio.Tasks[i].From = req.From
			studio.Tasks[i].AssignedNodeID = req.AssignedNodeID
			// Update input task IDs if provided
			if req.InputTaskIDs != nil {
				studio.Tasks[i].InputTaskIDs = req.InputTaskIDs
			}
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Debug("Updated task in studio", logger.Fields{"task_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task updated successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DeleteTask handles DELETE /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find and remove task
	found := false
	newTasks := make([]Task, 0)
	for _, task := range studio.Tasks {
		if task.ID != taskID {
			newTasks = append(newTasks, task)
		} else {
			found = true
		}
	}

	if !found {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	studio.Tasks = newTasks

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update studio: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Debug("Deleted task from studio", logger.Fields{"workspace_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task deleted successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// ExecuteTaskManually handles POST /api/studios/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}/execute
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find the task
	var targetTask *Task
	for i := range studio.Tasks {
		if studio.Tasks[i].ID == taskID {
			targetTask = &studio.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	logger.Debug("Manually executing task in studio", logger.Fields{"studioID": studioID, "workspace_id": taskID})

	// Execute task asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteTask(ctx, studioID, *targetTask); err != nil {
			logger.Error("Failed to execute task", logger.Fields{"task_id": taskID, "err": err})
		}
	}()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task execution started",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
