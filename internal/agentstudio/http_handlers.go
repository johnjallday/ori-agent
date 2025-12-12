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
		Attachments: make([]Attachment, 0),
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
		Description    *string   `json:"description,omitempty"`
		To             *string   `json:"to,omitempty"`
		From           *string   `json:"from,omitempty"`
		InputTaskIDs   *[]string `json:"input_task_ids,omitempty"`
		AssignedNodeID *string   `json:"assigned_node_id,omitempty"`
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

// CreateAttachment handles POST /api/studios/:id/attachments
func (h *HTTPHandler) CreateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	var req CreateAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "Attachment title is required", http.StatusBadRequest)
		return
	}

	attType := inferAttachmentType(&req)
	if attType != AttachmentTypeDoc && attType != AttachmentTypeImage && attType != AttachmentTypeOther {
		http.Error(w, "Attachment type must be one of: doc, image, other", http.StatusBadRequest)
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
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
		http.Error(w, fmt.Sprintf("Failed to add attachment: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
		return
	}

	// Publish event for live updates
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	attachment, err := studio.GetAttachment(attachmentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Apply updates
	if req.Title != nil {
		attachment.Title = *req.Title
	}
	if req.Body != nil {
		attachment.Body = *req.Body
	}
	if req.Type != nil {
		if *req.Type != AttachmentTypeDoc && *req.Type != AttachmentTypeImage && *req.Type != AttachmentTypeOther {
			http.Error(w, "Attachment type must be one of: doc, image, other", http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf("Failed to update attachment: %v", err), http.StatusInternalServerError)
		return
	}

	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	attachmentID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	if err := studio.DeleteAttachment(attachmentID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	var req CreateStoreNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Name == "" {
		http.Error(w, "Store node name is required", http.StatusBadRequest)
		return
	}
	if req.BaseDir == "" {
		http.Error(w, "Base directory is required", http.StatusBadRequest)
		return
	}
	if req.Format == "" {
		http.Error(w, "Format is required", http.StatusBadRequest)
		return
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "binary": true}
	if !validFormats[req.Format] {
		http.Error(w, "Format must be one of: json, text, markdown, binary", http.StatusBadRequest)
		return
	}

	// Validate write mode
	if req.WriteMode == "" {
		req.WriteMode = "overwrite" // Default
	}
	validModes := map[string]bool{"overwrite": true, "append": true}
	if !validModes[req.WriteMode] {
		http.Error(w, "Write mode must be one of: overwrite, append", http.StatusBadRequest)
		return
	}

	// Validate base directory - absolute paths allowed, relative paths must use these prefixes
	allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
	if err := ValidateBaseDir(req.BaseDir, allowedRelativeDirs); err != nil {
		http.Error(w, fmt.Sprintf("Invalid base directory: %v", err), http.StatusBadRequest)
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Generate unique canvas node ID
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
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]

	// Handle both /store-nodes/{id} and /canvas/store-nodes/{id} patterns
	var nodeID string
	if parts[1] == "canvas" && len(parts) >= 4 {
		nodeID = parts[3] // /canvas/store-nodes/{id}
	} else {
		nodeID = parts[2] // /store-nodes/{id}
	}

	var req UpdateStoreNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find store node by ID or CanvasNodeID
	var storeNode *StoreNode
	for i := range studio.StoreNodes {
		if studio.StoreNodes[i].ID == nodeID || studio.StoreNodes[i].CanvasNodeID == nodeID {
			storeNode = &studio.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		http.Error(w, "Store node not found", http.StatusNotFound)
		return
	}

	// Apply updates
	if req.Name != nil {
		storeNode.Name = *req.Name
	}
	if req.BaseDir != nil {
		// Re-validate base directory - absolute paths allowed, relative paths must use prefixes
		allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
		if err := ValidateBaseDir(*req.BaseDir, allowedRelativeDirs); err != nil {
			http.Error(w, fmt.Sprintf("Invalid base directory: %v", err), http.StatusBadRequest)
			return
		}
		storeNode.BaseDir = *req.BaseDir
	}
	if req.Format != nil {
		validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "binary": true}
		if !validFormats[*req.Format] {
			http.Error(w, "Format must be one of: json, text, markdown, binary", http.StatusBadRequest)
			return
		}
		storeNode.Format = *req.Format
	}
	if req.WriteMode != nil {
		validModes := map[string]bool{"overwrite": true, "append": true}
		if !validModes[*req.WriteMode] {
			http.Error(w, "Write mode must be one of: overwrite, append", http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	nodeID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find and remove store node
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
		http.Error(w, "Store node not found", http.StatusNotFound)
		return
	}

	// Remove from layout
	if studio.Layout != nil && studio.Layout.StorePositions != nil {
		delete(studio.Layout.StorePositions, canvasNodeID)
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save studio: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	studioID := parts[0]
	nodeID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Studio not found: %v", err), http.StatusNotFound)
		return
	}

	// Find store node
	var storeNode *StoreNode
	for i := range studio.StoreNodes {
		if studio.StoreNodes[i].ID == nodeID {
			storeNode = &studio.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		http.Error(w, "Store node not found", http.StatusNotFound)
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
