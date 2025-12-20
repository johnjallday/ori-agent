package agentstudio

import (
	"encoding/json"
	"fmt"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// HTTPHandler handles HTTP requests for Agent Studio
type HTTPHandler struct {
	store        Store
	orchestrator *Orchestrator
	eventBus     *EventBus
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(store Store, orchestrator *Orchestrator, eventBus *EventBus) *HTTPHandler {
	return &HTTPHandler{
		store:        store,
		orchestrator: orchestrator,
		eventBus:     eventBus,
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	var req CreateStudioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if encodeErr := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); encodeErr != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Validate request
	if req.Name == "" {
		if err := orihttp.RespondBadRequest(w, "Studio name is required"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
		return
	}
	if len(req.Agents) == 0 {
		if err := orihttp.RespondBadRequest(w, "At least one agent is required"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
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
		Attachments: make([]Attachment, 0),
		Status:      StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		logger.Error("Failed to save studio", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to create studio"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	studioID := strings.TrimSuffix(path, "/")

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
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
		"attachments":        studio.Attachments,
		"scheduled_tasks":    studio.ScheduledTasks, // Include scheduled tasks for scheduler nodes
		"store_nodes":        studio.StoreNodes,     // Include store nodes
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Get all studio IDs
	ids, err := h.store.List()
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to list studios: %v", err)); err != nil {
			logger.

				// Get studio details
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",

				// Verify studio exists
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	if _, err := h.store.Get(studioID); err != nil {
		if err := orihttp.RespondNotFound(w, "Studio not found"); err != nil {
			logger.

				// Set SSE headers
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",

				// Parse request body
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req AddAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.AgentName == "" {
		if err := orihttp.RespondBadRequest(w, "Agent name is required"); err != nil {
			logger.

				// Get studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Add agent using workspace method (creates stable AgentInstance)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := studio.AddAgent(req.AgentName); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to add agent: %v", err)); err != nil {
			logger.

				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to update studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID and agent identifier from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error":

			// Format: "name" or "name:instanceNumber"
			err})
		}
		return
	}
	studioID := parts[0]
	agentIdentifier := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Parse agent identifier to extract name and instance number
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var agentName string
	var instanceNumber int
	if strings.Contains(agentIdentifier, ":") {
		identParts := strings.SplitN(agentIdentifier, ":", 2)
		agentName = identParts[0]
		instanceNumber, err = strconv.Atoi(identParts[1])
		if err != nil {
			if err := orihttp.RespondBadRequest(w, "Invalid instance number format"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
	} else {
		agentName = agentIdentifier
		instanceNumber = 0
	}

	// Find the specific agent instance to remove
	var targetInstanceID string

	// If we have AgentInstances, always use them (even if no instance number provided)
	if len(studio.AgentInstances) > 0 {
		if instanceNumber > 0 {
			// Find by name and instance number
			for _, inst := range studio.AgentInstances {
				if inst.Name == agentName && inst.InstanceNumber == instanceNumber {
					targetInstanceID = inst.ID
					break
				}
			}
		} else {
			// No instance number provided - find first matching agent by name
			for _, inst := range studio.AgentInstances {
				if inst.Name == agentName {
					targetInstanceID = inst.ID
					instanceNumber = inst.InstanceNumber // For logging
					break
				}
			}
		}

		if targetInstanceID == "" {
			if err := orihttp.RespondNotFound(w, fmt.Sprintf("Agent instance %s not found", agentName)); err != nil {
				logger.Error(

					// Remove using new method (maintains stable node IDs, only unassigns tasks for THIS instance)
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		if err := studio.RemoveAgentInstance(targetInstanceID); err != nil {
			if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to remove agent instance: %v", err)); err != nil {
				logger.Error("Failed to write response",

					// LEGACY: No AgentInstances exist - use old method (removes first occurrence by name)
					logger.Fields{"error": err})
			}
			return
		}
	} else {

		if err := studio.RemoveAgent(agentName); err != nil {
			if err := orihttp.RespondNotFound(w, fmt.Sprintf("Failed to remove agent: %v", err)); err != nil {
				logger.Error(

					// Save updated studio
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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

// CreateAttachmentRequest represents the request to create an attachment
type CreateAttachmentRequest struct {
	Title   string              `json:"title"`
	Body    string              `json:"body"`
	Type    AttachmentType      `json:"type"`
	Color   string              `json:"color"`
	LinkURL string              `json:"link_url"`
	File    *AttachmentFileMeta `json:"file_meta"`
	X       float64             `json:"x"`
	Y       float64             `json:"y"`
}

// inferAttachmentType picks a sensible type when not provided by the client.
func inferAttachmentType(req *CreateAttachmentRequest) AttachmentType {
	if req == nil {
		return AttachmentTypeDoc
	}

	normalized := AttachmentType(strings.ToLower(string(req.Type)))
	if normalized == AttachmentTypeDoc || normalized == AttachmentTypeImage || normalized == AttachmentTypeOther {
		return normalized
	}

	// Infer from file metadata
	if req.File != nil {
		mime := strings.ToLower(req.File.Mime)
		if strings.HasPrefix(mime, "image/") {
			return AttachmentTypeImage
		}
		name := strings.ToLower(req.File.Name)
		if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") ||
			strings.HasSuffix(name, ".gif") || strings.HasSuffix(name, ".webp") || strings.HasSuffix(name, ".bmp") ||
			strings.HasSuffix(name, ".tif") || strings.HasSuffix(name, ".tiff") || strings.HasSuffix(name, ".svg") {
			return AttachmentTypeImage
		}
	}

	// Infer from link url
	if req.LinkURL != "" {
		link := strings.ToLower(req.LinkURL)
		if strings.HasSuffix(link, ".png") || strings.HasSuffix(link, ".jpg") || strings.HasSuffix(link, ".jpeg") ||
			strings.HasSuffix(link, ".gif") || strings.HasSuffix(link, ".webp") || strings.HasSuffix(link, ".bmp") ||
			strings.HasSuffix(link, ".tif") || strings.HasSuffix(link, ".tiff") || strings.HasSuffix(link, ".svg") {
			return AttachmentTypeImage
		}
	}

	return AttachmentTypeDoc
}

// CreateTask handles POST /api/studios/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",

				// Parse request body
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.

				// Validate request
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Description == "" {
		if err := orihttp.RespondBadRequest(w, "Task description is required"); err != nil {
			logger.

				// Note: From and To agents are optional - tasks can be created without connections
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to add task: %v", err)); err != nil {
			logger.

				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{

				// Parse request body
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	var req struct {
		Description    *string   `json:"description,omitempty"`
		To             *string   `json:"to,omitempty"`
		From           *string   `json:"from,omitempty"`
		InputTaskIDs   *[]string `json:"input_task_ids,omitempty"`
		AssignedNodeID *string   `json:"assigned_node_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.

				// Get studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find and update task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	found := false
	for i, task := range studio.Tasks {
		if task.ID == taskID {
			// Update only provided fields (allowing explicit empty values)
			if req.Description != nil {
				studio.Tasks[i].Description = *req.Description
			}
			if req.To != nil {
				studio.Tasks[i].To = *req.To
			}
			if req.From != nil {
				studio.Tasks[i].From = *req.From
			}
			if req.AssignedNodeID != nil {
				studio.Tasks[i].AssignedNodeID = *req.AssignedNodeID
			}
			if req.InputTaskIDs != nil {
				studio.Tasks[i].InputTaskIDs = *req.InputTaskIDs
			}
			found = true
			break
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.

				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{

				// Get studio
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find and remove task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.Error("Failed to write response",

				// Save updated studio
				logger.Fields{"error": err})
		}
		return
	}

	studio.Tasks = newTasks

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to update studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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

// CreateAttachment handles POST /api/studios/:id/attachments
func (h *HTTPHandler) CreateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req CreateAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Title == "" {
		if err := orihttp.RespondBadRequest(w, "Attachment title is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	attType := inferAttachmentType(&req)
	if attType != AttachmentTypeDoc && attType != AttachmentTypeImage && attType != AttachmentTypeOther {
		if err := orihttp.RespondBadRequest(w, "Attachment type must be one of: doc, image, other"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	attachment := Attachment{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		Title:       req.Title,
		Body:        req.Body,
		Type:        attType,
		Color:       req.Color,
		LinkURL:     req.LinkURL,
		File:        req.File,
		X:           req.X,
		Y:           req.Y,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := studio.AddAttachment(attachment); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Failed to add attachment: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.

				// Publish event for live updates
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	createdAttachment, _ := studio.GetAttachment(attachment.ID)
	if createdAttachment == nil {
		createdAttachment = &attachment
	}

	if h.eventBus != nil && createdAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentCreated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": createdAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Attachment created successfully",
		"attachment": createdAttachment,
		"studio":     studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UpdateAttachment handles PATCH /api/studios/:id/attachments/:attachment_id
func (h *HTTPHandler) UpdateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]
	attachmentID := parts[2]

	var req struct {
		Title   *string             `json:"title,omitempty"`
		Body    *string             `json:"body,omitempty"`
		Type    *AttachmentType     `json:"type,omitempty"`
		Color   *string             `json:"color,omitempty"`
		LinkURL *string             `json:"link_url,omitempty"`
		File    *AttachmentFileMeta `json:"file_meta,omitempty"`
		X       *float64            `json:"x,omitempty"`
		Y       *float64            `json:"y,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	attachment, err := studio.GetAttachment(attachmentID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, err.Error()); err != nil {
			logger.

				// Apply updates
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Title != nil {
		attachment.Title = *req.Title
	}
	if req.Body != nil {
		attachment.Body = *req.Body
	}
	if req.Type != nil {
		if *req.Type != AttachmentTypeDoc && *req.Type != AttachmentTypeImage && *req.Type != AttachmentTypeOther {
			if err := orihttp.RespondBadRequest(w, "Attachment type must be one of: doc, image, other"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
		attachment.Type = *req.Type
	} else if attachment.Type == "" {
		// If type is missing entirely, infer from current data
		inferred := inferAttachmentType(&CreateAttachmentRequest{
			Type:    attachment.Type,
			LinkURL: attachment.LinkURL,
			File:    attachment.File,
		})
		attachment.Type = inferred
	}
	if req.Color != nil {
		attachment.Color = *req.Color
	}
	if req.LinkURL != nil {
		attachment.LinkURL = *req.LinkURL
	}
	if req.File != nil {
		attachment.File = req.File
	}
	if req.X != nil {
		attachment.X = *req.X
	}
	if req.Y != nil {
		attachment.Y = *req.Y
	}

	if err := studio.UpdateAttachment(*attachment); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to update attachment: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	updatedAttachment, _ := studio.GetAttachment(attachmentID)
	if updatedAttachment == nil {
		updatedAttachment = attachment
	}

	if h.eventBus != nil && updatedAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": updatedAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Attachment updated successfully",
		"attachment": updatedAttachment,
		"studio":     studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DeleteAttachment handles DELETE /api/studios/:id/attachments/:attachment_id
func (h *HTTPHandler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]
	attachmentID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := studio.DeleteAttachment(attachmentID); err != nil {
		if err := orihttp.RespondNotFound(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentDeleted,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment_id": attachmentID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Attachment deleted successfully",
		"attachment_id": attachmentID,
		"studio":        studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// ExecuteTaskManually handles POST /api/studios/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}/execute
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{

				// Get studio
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find the task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var targetTask *Task
	for i := range studio.Tasks {
		if studio.Tasks[i].ID == taskID {
			targetTask = &studio.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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

// CreateStoreNodeRequest represents the request to create a store node
type CreateStoreNodeRequest struct {
	Name          string  `json:"name"`
	BaseDir       string  `json:"base_dir"`
	Format        string  `json:"format"`
	WriteMode     string  `json:"write_mode"`
	AutoCreateDir bool    `json:"auto_create_dir"`
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
}

// CreateStoreNode handles POST /api/studios/:id/store-nodes
func (h *HTTPHandler) CreateStoreNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req CreateStoreNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.

				// Validate required fields
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Name == "" {
		if err := orihttp.RespondBadRequest(w, "Store node name is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	if req.BaseDir == "" {
		if err := orihttp.RespondBadRequest(w, "Base directory is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	if req.Format == "" {
		if err := orihttp.RespondBadRequest(w, "Format is required"); err != nil {
			logger.

				// Validate format
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "binary": true}
	if !validFormats[req.Format] {
		if err := orihttp.RespondBadRequest(w, "Format must be one of: json, text, markdown, binary"); err != nil {
			logger.

				// Validate write mode
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.WriteMode == "" {
		req.WriteMode = "overwrite" // Default
	}
	validModes := map[string]bool{"overwrite": true, "append": true}
	if !validModes[req.WriteMode] {
		if err := orihttp.RespondBadRequest(w, "Write mode must be one of: overwrite, append"); err != nil {
			logger.

				// Validate base directory - absolute paths allowed, relative paths must use these prefixes
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
	if err := ValidateBaseDir(req.BaseDir, allowedRelativeDirs); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid base directory: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Generate unique canvas node ID
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	canvasNodeID := fmt.Sprintf("store-node-%d", len(studio.StoreNodes)+1)

	storeNode := StoreNode{
		ID:            uuid.New().String(),
		CanvasNodeID:  canvasNodeID,
		WorkspaceID:   studioID,
		Name:          req.Name,
		BaseDir:       req.BaseDir,
		Format:        req.Format,
		WriteMode:     req.WriteMode,
		AutoCreateDir: req.AutoCreateDir,
		AutoStore:     true,        // Default to enabled for automatic task result storage
		LastWriteTime: time.Time{}, // Zero value
		WriteCount:    0,
		LastError:     "",
		LastFilePath:  "",
		X:             req.X,
		Y:             req.Y,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Add to workspace
	studio.StoreNodes = append(studio.StoreNodes, storeNode)

	// Initialize layout if needed
	if studio.Layout == nil {
		studio.Layout = &CanvasLayout{
			StorePositions: make(map[string]Position),
		}
	}
	if studio.Layout.StorePositions == nil {
		studio.Layout.StorePositions = make(map[string]Position)
	}

	// Set position in layout
	studio.Layout.StorePositions[storeNode.CanvasNodeID] = Position{X: req.X, Y: req.Y}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Created store node in studio", logger.Fields{"store_node_id": storeNode.ID, "studioID": studioID, "name": storeNode.Name})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.created",
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"store_node_id": storeNode.ID,
				"name":          storeNode.Name,
			},
		})
	}

	// Return created store node
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(storeNode); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetStoreNodes handles GET /api/studios/:id/store-nodes
func (h *HTTPHandler) GetStoreNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(studio.StoreNodes); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UpdateStoreNodeRequest represents the request to update a store node
type UpdateStoreNodeRequest struct {
	Name          *string  `json:"name"`
	BaseDir       *string  `json:"base_dir"`
	Format        *string  `json:"format"`
	WriteMode     *string  `json:"write_mode"`
	AutoCreateDir *bool    `json:"auto_create_dir"`
	AutoStore     *bool    `json:"auto_store"`
	AgentNodeID   *string  `json:"agent_node_id"`
	X             *float64 `json:"x"`
	Y             *float64 `json:"y"`
}

// UpdateStoreNode handles PUT /api/studios/:id/store-nodes/:node_id
func (h *HTTPHandler) UpdateStoreNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",

				// Handle both /store-nodes/{id} and /canvas/store-nodes/{id} patterns
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var nodeID string
	if parts[1] == "canvas" && len(parts) >= 4 {
		nodeID = parts[3] // /canvas/store-nodes/{id}
	} else {
		nodeID = parts[2] // /store-nodes/{id}
	}

	var req UpdateStoreNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find store node by ID or CanvasNodeID
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var storeNode *StoreNode
	for i := range studio.StoreNodes {
		if studio.StoreNodes[i].ID == nodeID || studio.StoreNodes[i].CanvasNodeID == nodeID {
			storeNode = &studio.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		if err := orihttp.RespondNotFound(w, "Store node not found"); err != nil {
			logger.

				// Apply updates
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Name != nil {
		storeNode.Name = *req.Name
	}
	if req.BaseDir != nil {
		// Re-validate base directory - absolute paths allowed, relative paths must use prefixes
		allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
		if err := ValidateBaseDir(*req.BaseDir, allowedRelativeDirs); err != nil {
			if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid base directory: %v", err)); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
		storeNode.BaseDir = *req.BaseDir
	}
	if req.Format != nil {
		validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "binary": true}
		if !validFormats[*req.Format] {
			if err := orihttp.RespondBadRequest(w, "Format must be one of: json, text, markdown, binary"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
		storeNode.Format = *req.Format
	}
	if req.WriteMode != nil {
		validModes := map[string]bool{"overwrite": true, "append": true}
		if !validModes[*req.WriteMode] {
			if err := orihttp.RespondBadRequest(w, "Write mode must be one of: overwrite, append"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
		storeNode.WriteMode = *req.WriteMode
	}
	if req.AutoCreateDir != nil {
		storeNode.AutoCreateDir = *req.AutoCreateDir
	}
	if req.AgentNodeID != nil {
		storeNode.AgentNodeID = *req.AgentNodeID
	}
	if req.AutoStore != nil {
		storeNode.AutoStore = *req.AutoStore
	}

	// Update position if provided
	if req.X != nil || req.Y != nil {
		if studio.Layout == nil {
			studio.Layout = &CanvasLayout{StorePositions: make(map[string]Position)}
		}
		if studio.Layout.StorePositions == nil {
			studio.Layout.StorePositions = make(map[string]Position)
		}

		currentPos := studio.Layout.StorePositions[storeNode.CanvasNodeID]
		if req.X != nil {
			currentPos.X = *req.X
			storeNode.X = *req.X
		}
		if req.Y != nil {
			currentPos.Y = *req.Y
			storeNode.Y = *req.Y
		}
		studio.Layout.StorePositions[storeNode.CanvasNodeID] = currentPos
	}

	storeNode.UpdatedAt = time.Now()

	// Save studio
	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Updated store node", logger.Fields{"store_node_id": nodeID, "studioID": studioID})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.updated",
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"store_node_id": nodeID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(storeNode); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DeleteStoreNode handles DELETE /api/studios/:id/store-nodes/:node_id
func (h *HTTPHandler) DeleteStoreNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]
	nodeID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find and remove store node
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	found := false
	var canvasNodeID string
	for i := range studio.StoreNodes {
		if studio.StoreNodes[i].ID == nodeID {
			canvasNodeID = studio.StoreNodes[i].CanvasNodeID
			studio.StoreNodes = append(studio.StoreNodes[:i], studio.StoreNodes[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, "Store node not found"); err != nil {
			logger.

				// Remove from layout
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if studio.Layout != nil && studio.Layout.StorePositions != nil {
		delete(studio.Layout.StorePositions, canvasNodeID)
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Deleted store node", logger.Fields{"store_node_id": nodeID, "studioID": studioID})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.deleted",
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"store_node_id": nodeID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Store node deleted successfully",
		"store_node_id": nodeID,
		"studio":        studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetStoreNodeStatus handles GET /api/studios/:id/store-nodes/:node_id/status
func (h *HTTPHandler) GetStoreNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]
	nodeID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.

				// Find store node
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var storeNode *StoreNode
	for i := range studio.StoreNodes {
		if studio.StoreNodes[i].ID == nodeID {
			storeNode = &studio.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		if err := orihttp.RespondNotFound(w, "Store node not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"last_write_time": storeNode.LastWriteTime,
		"write_count":     storeNode.WriteCount,
		"last_error":      storeNode.LastError,
		"last_file_path":  storeNode.LastFilePath,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
