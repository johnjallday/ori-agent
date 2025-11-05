package agentstudio

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
	Mission     string   `json:"mission,omitempty"` // Optional: execute mission immediately
}

// ExecuteMissionRequest represents the request to execute a mission
type ExecuteMissionRequest struct {
	Mission string `json:"mission"`
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
		ParentAgent: "system",
		Agents:      req.Agents,
		SharedData:  make(map[string]interface{}),
		Messages:    make([]AgentMessage, 0),
		Tasks:       make([]Task, 0),
		Status:      StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Add mission to shared data if provided
	if req.Mission != "" {
		studio.SharedData["mission"] = req.Mission
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		log.Printf("Failed to save studio: %v", err)
		http.Error(w, "Failed to create studio", http.StatusInternalServerError)
		return
	}

	log.Printf("Created studio: %s (ID: %s) with agents: %v", studio.Name, studio.ID, studio.Agents)

	// Execute mission if provided
	if req.Mission != "" {
		go func() {
			ctx := r.Context()
			if err := h.orchestrator.ExecuteMission(ctx, studio.ID, req.Mission); err != nil {
				log.Printf("Failed to execute mission for studio %s: %v", studio.ID, err)
			}
		}()
		log.Printf("Started mission execution for studio %s: %s", studio.ID, req.Mission)
	}

	// Return created studio
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      studio.ID,
		"name":    studio.Name,
		"agents":  studio.Agents,
		"status":  studio.Status,
		"message": "Studio created successfully",
	})
}

// ExecuteMission handles POST /api/studios/:id/mission
func (h *HTTPHandler) ExecuteMission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID from URL path
	// URL format: /api/studios/{id}/mission
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	// Verify studio exists
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Parse request
	var req ExecuteMissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Mission == "" {
		http.Error(w, "Mission is required", http.StatusBadRequest)
		return
	}

	// Store mission in shared data
	studio.SetSharedData("mission", req.Mission)
	if err := h.store.Save(studio); err != nil {
		log.Printf("Failed to save mission to studio: %v", err)
	}

	// Execute mission asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteMission(ctx, studioID, req.Mission); err != nil {
			log.Printf("Failed to execute mission for studio %s: %v", studioID, err)
		}
	}()

	log.Printf("Started mission execution for studio %s: %s", studioID, req.Mission)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Mission execution started",
		"mission": req.Mission,
		"studio":  studioID,
	})
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

	// Return studio details
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          studio.ID,
		"name":        studio.Name,
		"description": studio.Description,
		"agents":      studio.Agents,
		"status":      studio.Status,
		"tasks":       studio.Tasks,
		"messages":    studio.Messages,
		"shared_data": studio.SharedData,
		"created_at":  studio.CreatedAt,
		"updated_at":  studio.UpdatedAt,
	})
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
			log.Printf("Warning: Failed to get studio %s: %v", id, err)
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"studios": studios,
		"count":   len(studios),
	})
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
			Metadata:    make(map[string]string),
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

	// Check if agent already exists
	for _, agent := range studio.Agents {
		if agent == req.AgentName {
			http.Error(w, "Agent already in workspace", http.StatusConflict)
			return
		}
	}

	// Add agent to workspace
	studio.Agents = append(studio.Agents, req.AgentName)

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update studio: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Added agent %s to studio %s", req.AgentName, studioID)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"studio":  studioID,
	})
}

// RemoveAgent handles DELETE /api/studios/:id/agents/:agent_name
func (h *HTTPHandler) RemoveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract studio ID and agent name from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	agentName := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find and remove agent
	found := false
	newAgents := make([]string, 0)
	for _, agent := range studio.Agents {
		if agent != agentName {
			newAgents = append(newAgents, agent)
		} else {
			found = true
		}
	}

	if !found {
		http.Error(w, "Agent not found in workspace", http.StatusNotFound)
		return
	}

	studio.Agents = newAgents

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update studio: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Removed agent %s from studio %s", agentName, studioID)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent removed successfully",
		"agent":   agentName,
		"studio":  studioID,
	})
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
	if req.From == "" {
		http.Error(w, "From agent is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "To agent is required", http.StatusBadRequest)
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	log.Printf("[DEBUG] CreateTask - Studio: %s, ParentAgent: %s, Agents: %v", studioID, studio.ParentAgent, studio.Agents)
	log.Printf("[DEBUG] CreateTask - Request: From=%s, To=%s", req.From, req.To)

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
		log.Printf("[DEBUG] CreateTask - AddTask failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to add task: %v", err), http.StatusInternalServerError)
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Created task %s in studio %s: %s", task.ID, studioID, req.Description)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task created successfully",
		"task_id": task.ID,
		"task":    task,
		"studio":  studioID,
	})
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

	log.Printf("Deleted task %s from studio %s", taskID, studioID)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task deleted successfully",
		"task_id": taskID,
		"studio":  studioID,
	})
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

	log.Printf("Manually executing task %s in studio %s", taskID, studioID)

	// Execute task asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteTask(ctx, studioID, *targetTask); err != nil {
			log.Printf("Failed to execute task %s: %v", taskID, err)
		}
	}()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task execution started",
		"task_id": taskID,
		"studio":  studioID,
	})
}
