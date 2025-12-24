package agentstudio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

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
