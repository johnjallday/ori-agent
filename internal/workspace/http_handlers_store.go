package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// CreateStoreNodeRequest represents the request to create a store node
type CreateStoreNodeRequest struct {
	Name          string  `json:"name"`
	BaseDir       string  `json:"base_dir"`
	StorageTarget string  `json:"storage_target"`
	Folder        string  `json:"workspace_folder"`
	Format        string  `json:"format"`
	WriteMode     string  `json:"write_mode"`
	AutoCreateDir bool    `json:"auto_create_dir"`
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
}

// CreateStoreNode handles POST /api/workspaces/:id/store-nodes
func (h *HTTPHandler) CreateStoreNode(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	var req CreateStoreNodeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate required fields

	if req.Name == "" {
		orihttp.BadRequest(w, "Store node name is required")
		return
	}
	storageTarget := NormalizeStorageTarget(req.StorageTarget)
	if req.BaseDir == "" && storageTarget != StorageTargetWorkspaceFolder {
		orihttp.BadRequest(w, "Base directory is required")
		return
	}
	if req.Format == "" {
		orihttp.BadRequest(w, "Format is required")
		return
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "csv": true, "binary": true}
	if !validFormats[req.Format] {
		orihttp.BadRequest(w, "Format must be one of: json, text, markdown, csv, binary")
		return
	}

	// Validate write mode
	if req.WriteMode == "" {
		req.WriteMode = "overwrite" // Default
	}
	validModes := map[string]bool{"overwrite": true, "append": true}
	if !validModes[req.WriteMode] {
		orihttp.BadRequest(w, "Write mode must be one of: overwrite, append")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	workspaceFolder := ""
	if storageTarget == StorageTargetWorkspaceFolder {
		absFolder, clean, err := workspaceFolderPathWithinRoot(h.store.GetFilesPath(workspaceID), req.Folder)
		if err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Invalid workspace folder: %v", err))
			return
		}
		if err := os.MkdirAll(absFolder, 0755); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to create workspace folder: %v", err))
			return
		}
		workspaceFolder = clean
		req.BaseDir = clean
	} else {
		// Validate base directory - absolute paths allowed, relative paths must use these prefixes
		allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
		if err := ValidateBaseDir(req.BaseDir, allowedRelativeDirs); err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Invalid base directory: %v", err))
			return
		}
	}

	// Generate unique canvas node ID

	canvasNodeID := fmt.Sprintf("store-node-%d", len(workspace.StoreNodes)+1)

	storeNode := StoreNode{
		ID:            uuid.New().String(),
		CanvasNodeID:  canvasNodeID,
		WorkspaceID:   workspaceID,
		Name:          req.Name,
		BaseDir:       req.BaseDir,
		StorageTarget: storageTarget,
		Folder:        workspaceFolder,
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
	workspace.StoreNodes = append(workspace.StoreNodes, storeNode)

	// Initialize layout if needed
	if workspace.Layout == nil {
		workspace.Layout = &CanvasLayout{
			StorePositions: make(map[string]Position),
		}
	}
	if workspace.Layout.StorePositions == nil {
		workspace.Layout.StorePositions = make(map[string]Position)
	}

	// Set position in layout
	workspace.Layout.StorePositions[storeNode.CanvasNodeID] = Position{X: req.X, Y: req.Y}

	// Save workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Info("Created store node in workspace", logger.Fields{"store_node_id": storeNode.ID, "workspaceID": workspaceID, "name": storeNode.Name})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.created",
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"store_node_id": storeNode.ID,
				"name":          storeNode.Name,
			},
		})
	}

	// Return created store node
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(storeNode); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetStoreNodes handles GET /api/workspaces/:id/store-nodes
func (h *HTTPHandler) GetStoreNodes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(workspace.StoreNodes); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateStoreNodeRequest represents the request to update a store node
type UpdateStoreNodeRequest struct {
	Name          *string  `json:"name"`
	BaseDir       *string  `json:"base_dir"`
	StorageTarget *string  `json:"storage_target"`
	Folder        *string  `json:"workspace_folder"`
	Format        *string  `json:"format"`
	WriteMode     *string  `json:"write_mode"`
	AutoCreateDir *bool    `json:"auto_create_dir"`
	AutoStore     *bool    `json:"auto_store"`
	AgentNodeID   *string  `json:"agent_node_id"`
	X             *float64 `json:"x"`
	Y             *float64 `json:"y"`
}

// UpdateStoreNode handles PUT /api/workspaces/:id/store-nodes/:node_id
func (h *HTTPHandler) UpdateStoreNode(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	nodeID := r.PathValue("nodeId")

	var req UpdateStoreNodeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find store node by ID or CanvasNodeID
	var storeNode *StoreNode
	for i := range workspace.StoreNodes {
		if workspace.StoreNodes[i].ID == nodeID || workspace.StoreNodes[i].CanvasNodeID == nodeID {
			storeNode = &workspace.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		orihttp.NotFound(w, "Store node not found")
		return
	}

	// Apply updates

	if req.Name != nil {
		storeNode.Name = *req.Name
	}
	if req.StorageTarget != nil || req.Folder != nil || req.BaseDir != nil {
		nextTarget := storeNode.StorageTarget
		if req.StorageTarget != nil {
			nextTarget = NormalizeStorageTarget(*req.StorageTarget)
		}

		if nextTarget == StorageTargetWorkspaceFolder {
			rawFolder := storeNode.Folder
			if req.Folder != nil {
				rawFolder = *req.Folder
			} else if req.BaseDir != nil {
				rawFolder = *req.BaseDir
			}
			absFolder, clean, err := workspaceFolderPathWithinRoot(h.store.GetFilesPath(workspaceID), rawFolder)
			if err != nil {
				orihttp.BadRequest(w, fmt.Sprintf("Invalid workspace folder: %v", err))
				return
			}
			if err := os.MkdirAll(absFolder, 0755); err != nil {
				orihttp.InternalError(w, fmt.Sprintf("Failed to create workspace folder: %v", err))
				return
			}
			storeNode.StorageTarget = StorageTargetWorkspaceFolder
			storeNode.Folder = clean
			storeNode.BaseDir = clean
		} else {
			if req.BaseDir != nil {
				// Re-validate base directory - absolute paths allowed, relative paths must use prefixes
				allowedRelativeDirs := []string{"agents/", "outputs/", "reports/", "data/"}
				if err := ValidateBaseDir(*req.BaseDir, allowedRelativeDirs); err != nil {
					orihttp.BadRequest(w, fmt.Sprintf("Invalid base directory: %v", err))
					return
				}
				storeNode.BaseDir = *req.BaseDir
			}
			storeNode.StorageTarget = ""
			storeNode.Folder = ""
		}
	}
	if req.Format != nil {
		validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "csv": true, "binary": true}
		if !validFormats[*req.Format] {
			orihttp.BadRequest(w, "Format must be one of: json, text, markdown, csv, binary")
			return
		}
		storeNode.Format = *req.Format
	}
	if req.WriteMode != nil {
		validModes := map[string]bool{"overwrite": true, "append": true}
		if !validModes[*req.WriteMode] {
			orihttp.BadRequest(w, "Write mode must be one of: overwrite, append")
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
		if workspace.Layout == nil {
			workspace.Layout = &CanvasLayout{StorePositions: make(map[string]Position)}
		}
		if workspace.Layout.StorePositions == nil {
			workspace.Layout.StorePositions = make(map[string]Position)
		}

		currentPos := workspace.Layout.StorePositions[storeNode.CanvasNodeID]
		if req.X != nil {
			currentPos.X = *req.X
			storeNode.X = *req.X
		}
		if req.Y != nil {
			currentPos.Y = *req.Y
			storeNode.Y = *req.Y
		}
		workspace.Layout.StorePositions[storeNode.CanvasNodeID] = currentPos
	}

	storeNode.UpdatedAt = time.Now()

	// Save workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Debug("Updated store node", logger.Fields{"store_node_id": nodeID, "workspaceID": workspaceID})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.updated",
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"store_node_id": nodeID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(storeNode); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteStoreNode handles DELETE /api/workspaces/:id/store-nodes/:node_id
func (h *HTTPHandler) DeleteStoreNode(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	nodeID := r.PathValue("nodeId")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find and remove store node
	found := false
	var canvasNodeID string
	for i := range workspace.StoreNodes {
		if workspace.StoreNodes[i].ID == nodeID {
			canvasNodeID = workspace.StoreNodes[i].CanvasNodeID
			workspace.StoreNodes = append(workspace.StoreNodes[:i], workspace.StoreNodes[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		orihttp.NotFound(w, "Store node not found")
		return
	}

	// Remove from layout
	if workspace.Layout != nil && workspace.Layout.StorePositions != nil {
		delete(workspace.Layout.StorePositions, canvasNodeID)
	}

	// Save workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Debug("Deleted store node", logger.Fields{"store_node_id": nodeID, "workspaceID": workspaceID})

	// Publish event
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        "store_node.deleted",
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"store_node_id": nodeID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Store node deleted successfully",
		"store_node_id": nodeID,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetStoreNodeStatus handles GET /api/workspaces/:id/store-nodes/:node_id/status
func (h *HTTPHandler) GetStoreNodeStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	nodeID := r.PathValue("nodeId")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find store node
	var storeNode *StoreNode
	for i := range workspace.StoreNodes {
		if workspace.StoreNodes[i].ID == nodeID {
			storeNode = &workspace.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		orihttp.NotFound(w, "Store node not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"last_write_time": storeNode.LastWriteTime,
		"write_count":     storeNode.WriteCount,
		"last_error":      storeNode.LastError,
		"last_file_path":  storeNode.LastFilePath,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetWorkspaceOutputDir handles GET /api/workspaces/:id/output-dir
// Returns the default output directory for a workspace
func (h *HTTPHandler) GetWorkspaceOutputDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Default output directory: the workspace's own folder (<workspace>/outputs/).
	outputDir := h.store.GetOutputsPath(workspaceID)
	if outputDir == "" {
		// Fallback to the global output directory if the workspace folder
		// can't be resolved.
		baseOutputDir, err := platform.GetDefaultOutputDir()
		if err != nil {
			baseOutputDir = "outputs"
			logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": err})
		}
		outputDir = filepath.Join(baseOutputDir, workspace.Name)
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"output_dir":   outputDir,
		"workspace_id": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// OpenWorkspaceOutputDir handles POST /api/workspaces/:id/output-dir/open
//
// Opens the workspace's default outputs folder in the OS file manager
// (Finder on macOS, Explorer on Windows). Creates the directory if it
// doesn't exist yet so the user can drop files in immediately.
func (h *HTTPHandler) OpenWorkspaceOutputDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	outputDir := h.store.GetOutputsPath(workspaceID)
	if outputDir == "" {
		baseOutputDir, derr := platform.GetDefaultOutputDir()
		if derr != nil {
			baseOutputDir = "outputs"
			logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": derr})
		}
		outputDir = filepath.Join(baseOutputDir, workspace.Name)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to create output directory: %v", err))
		return
	}

	if err := platform.OpenFolder(outputDir); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to open output directory: %v", err))
		return
	}

	logger.Info("Opened workspace output directory", logger.Fields{
		"workspace_id": workspaceID,
		"path":         outputDir,
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"output_dir":   outputDir,
		"workspace_id": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
