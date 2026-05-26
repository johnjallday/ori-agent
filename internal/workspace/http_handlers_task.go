package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// CreateTaskRequest represents the request to create a task
type CreateTaskRequest struct {
	Description            string               `json:"description"`
	From                   string               `json:"from"`
	To                     string               `json:"to"`
	Priority               int                  `json:"priority"`
	ParentTaskID           string               `json:"parent_task_id"`
	SubtaskIndex           int                  `json:"subtask_index"`
	OrchestrationMode      string               `json:"orchestration_mode"`
	ResultCombinationMode  string               `json:"result_combination_mode"`
	CombinationInstruction string               `json:"combination_instruction"`
	OutputSchema           *TaskOutputSchema    `json:"output_schema"`
	OutputContract         *TaskOutputContract  `json:"output_contract"`
	OutputSpec             *TaskOutputSpec      `json:"output_spec"`
	DraftOutputSpec        *TaskOutputSpec      `json:"draft_output_spec"`
	ResultStorage          *ResultStorageConfig `json:"result_storage"`
	TemplateRef            *TaskTemplateRef     `json:"template_ref"`
}

// CreateTask handles POST /api/workspaces/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	// Parse request body

	var req CreateTaskRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate request
	// Note: From and To agents are optional - tasks can be created without connections
	if req.Description == "" {
		orihttp.BadRequest(w, "Task description is required")
		return
	}

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	logger.Debug("CreateTask - Workspace and agents", logger.Fields{"workspace_id": workspaceID, "agents": workspace.Agents})
	logger.Debug("CreateTask - Request routing", logger.Fields{"from": req.From, "to": req.To})

	outputSpec, outputSpecErrors := NormalizeTaskOutputSpec(req.OutputSpec)
	if len(outputSpecErrors) > 0 {
		orihttp.BadRequest(w, "Invalid output_spec: "+strings.Join(outputSpecErrors, "; "))
		return
	}
	draftOutputSpec, draftSpecErrors := NormalizeTaskOutputSpec(req.DraftOutputSpec)
	if len(draftSpecErrors) > 0 {
		orihttp.BadRequest(w, "Invalid draft_output_spec: "+strings.Join(draftSpecErrors, "; "))
		return
	}

	// Create task
	task := Task{
		ID:                     uuid.New().String(),
		WorkspaceID:            workspaceID,
		From:                   req.From,
		To:                     req.To,
		Description:            req.Description,
		Priority:               req.Priority,
		Context:                make(map[string]any),
		ParentTaskID:           req.ParentTaskID,
		SubtaskIndex:           req.SubtaskIndex,
		OrchestrationMode:      NormalizeTaskOrchestrationMode(req.OrchestrationMode),
		ResultCombinationMode:  NormalizeTaskResultCombinationMode(req.ResultCombinationMode),
		CombinationInstruction: strings.TrimSpace(req.CombinationInstruction),
		OutputSchema:           NormalizeTaskOutputSchema(req.OutputSchema),
		OutputContract:         NormalizeTaskOutputContract(req.OutputContract),
		OutputSpec:             outputSpec,
		DraftOutputSpec:        draftOutputSpec,
		ResultStorage:          req.ResultStorage,
		TemplateRef:            req.TemplateRef,
		Status:                 TaskStatusPending,
		CreatedAt:              time.Now(),
	}
	if task.OutputSpec != nil {
		task.OutputSchema = task.OutputSpec.Schema
		task.OutputContract = task.OutputSpec.Contract
	}

	// Add task to workspace
	if err := workspace.AddTask(task); err != nil {
		logger.Error("[DEBUG] CreateTask - AddTask failed", logger.Fields{"task_id": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to add task: %v", err))
		return
	}

	// Save updated workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Info("Created task in workspace", logger.Fields{"description": req.Description, "task_id": task.ID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Task created successfully",
		"task_id":   task.ID,
		"task":      task,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateTask handles PATCH /api/workspaces/:id/tasks/:task_id
func (h *HTTPHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Parse request body

	var req struct {
		Description            *string              `json:"description,omitempty"`
		Details                *string              `json:"details,omitempty"`
		To                     *string              `json:"to,omitempty"`
		From                   *string              `json:"from,omitempty"`
		InputTaskIDs           *[]string            `json:"input_task_ids,omitempty"`
		AssignedNodeID         *string              `json:"assigned_node_id,omitempty"`
		ParentTaskID           *string              `json:"parent_task_id,omitempty"`
		SubtaskIndex           *int                 `json:"subtask_index,omitempty"`
		OrchestrationMode      *string              `json:"orchestration_mode,omitempty"`
		ResultCombinationMode  *string              `json:"result_combination_mode,omitempty"`
		CombinationInstruction *string              `json:"combination_instruction,omitempty"`
		OutputSchema           *TaskOutputSchema    `json:"output_schema,omitempty"`
		OutputContract         *TaskOutputContract  `json:"output_contract,omitempty"`
		OutputSpec             *TaskOutputSpec      `json:"output_spec,omitempty"`
		DraftOutputSpec        *TaskOutputSpec      `json:"draft_output_spec,omitempty"`
		ResultStorage          *ResultStorageConfig `json:"result_storage,omitempty"`
		TemplateRef            *TaskTemplateRef     `json:"template_ref,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find and update task
	found := false
	for i, task := range workspace.Tasks {
		if task.ID == taskID {
			// Update only provided fields (allowing explicit empty values)
			if req.Description != nil {
				workspace.Tasks[i].Description = *req.Description
			}
			if req.Details != nil {
				workspace.Tasks[i].Details = *req.Details
			}
			if req.To != nil {
				workspace.Tasks[i].To = *req.To
			}
			if req.From != nil {
				workspace.Tasks[i].From = *req.From
			}
			if req.AssignedNodeID != nil {
				workspace.Tasks[i].AssignedNodeID = *req.AssignedNodeID
			}
			if req.InputTaskIDs != nil {
				workspace.Tasks[i].InputTaskIDs = *req.InputTaskIDs
			}
			if req.ParentTaskID != nil {
				workspace.Tasks[i].ParentTaskID = strings.TrimSpace(*req.ParentTaskID)
				if workspace.Tasks[i].ParentTaskID == "" {
					workspace.Tasks[i].SubtaskIndex = 0
				}
			}
			if req.SubtaskIndex != nil {
				workspace.Tasks[i].SubtaskIndex = *req.SubtaskIndex
			}
			if req.OrchestrationMode != nil {
				workspace.Tasks[i].OrchestrationMode = NormalizeTaskOrchestrationMode(*req.OrchestrationMode)
			}
			if req.ResultCombinationMode != nil {
				workspace.Tasks[i].ResultCombinationMode = NormalizeTaskResultCombinationMode(*req.ResultCombinationMode)
			}
			if req.CombinationInstruction != nil {
				workspace.Tasks[i].CombinationInstruction = strings.TrimSpace(*req.CombinationInstruction)
			}
			if req.OutputSchema != nil {
				workspace.Tasks[i].OutputSchema = NormalizeTaskOutputSchema(req.OutputSchema)
			}
			if req.OutputContract != nil {
				workspace.Tasks[i].OutputContract = NormalizeTaskOutputContract(req.OutputContract)
			}
			if req.OutputSpec != nil {
				outputSpec, outputSpecErrors := NormalizeTaskOutputSpec(req.OutputSpec)
				if len(outputSpecErrors) > 0 {
					orihttp.BadRequest(w, "Invalid output_spec: "+strings.Join(outputSpecErrors, "; "))
					return
				}
				workspace.Tasks[i].OutputSpec = outputSpec
				if outputSpec != nil {
					workspace.Tasks[i].OutputSchema = outputSpec.Schema
					workspace.Tasks[i].OutputContract = outputSpec.Contract
				}
			}
			if req.DraftOutputSpec != nil {
				draftOutputSpec, draftSpecErrors := NormalizeTaskOutputSpec(req.DraftOutputSpec)
				if len(draftSpecErrors) > 0 {
					orihttp.BadRequest(w, "Invalid draft_output_spec: "+strings.Join(draftSpecErrors, "; "))
					return
				}
				workspace.Tasks[i].DraftOutputSpec = draftOutputSpec
			}
			if req.ResultStorage != nil {
				workspace.Tasks[i].ResultStorage = req.ResultStorage
			}
			if req.TemplateRef != nil {
				workspace.Tasks[i].TemplateRef = req.TemplateRef
			}
			found = true
			break
		}
	}

	if !found {
		orihttp.NotFound(w, "Task not found")
		return
	}

	// Save updated workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Debug("Updated task in workspace", logger.Fields{"task_id": taskID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Task updated successfully",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteTask handles DELETE /api/workspaces/:id/tasks/:task_id
func (h *HTTPHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	if err := workspace.DeleteTask(taskID); err != nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update workspace: %v", err))
		return
	}

	logger.Debug("Deleted task from workspace", logger.Fields{"workspace_id": taskID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Task deleted successfully",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ExecuteTaskManually handles POST /api/workspaces/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}/execute
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find the task
	var targetTask *Task
	for i := range workspace.Tasks {
		if workspace.Tasks[i].ID == taskID {
			targetTask = &workspace.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	logger.Debug("Manually executing task in workspace", logger.Fields{"workspaceID": workspaceID, "workspace_id": taskID})

	// Execute task asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteTask(ctx, workspaceID, *targetTask); err != nil {
			logger.Error("Failed to execute task", logger.Fields{"task_id": taskID, "err": err})
		}
	}()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Task execution started",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// AppendResultToCSVRequest is the body for POST .../results/append-csv.
type AppendResultToCSVRequest struct {
	CSV         string `json:"csv"`
	UseStorage  bool   `json:"use_storage"`
	FilePath    string `json:"file_path"`
	StoreNodeID string `json:"store_node_id"`
}

// AppendResultToCSV handles POST /api/workspaces/:id/tasks/:task_id/results/append-csv.
// It appends a CSV payload from a task's result artifact either to the task's
// already-configured result storage destination (use_storage=true) or to a
// one-shot destination supplied in the request.
func (h *HTTPHandler) AppendResultToCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}/results/append-csv
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	var req AppendResultToCSVRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	csvData := strings.TrimSpace(req.CSV)
	if csvData == "" {
		orihttp.BadRequest(w, "csv body is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	var task *Task
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			task = &ws.Tasks[i]
			break
		}
	}
	if task == nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	storeNodeID := strings.TrimSpace(req.StoreNodeID)
	filePath := strings.TrimSpace(req.FilePath)

	var storageCfg *ResultStorageConfig
	if owner := ResolveTaskResultStorageOwner(ws, task); owner != nil {
		storageCfg = owner.ResultStorage
	}

	if req.UseStorage {
		if storageCfg == nil || !storageCfg.Enabled || strings.ToLower(strings.TrimSpace(storageCfg.WriteMode)) != "append" {
			orihttp.BadRequest(w, "Task is not configured to append results to a CSV file")
			return
		}
		storeNodeID = strings.TrimSpace(storageCfg.StoreNodeID)
		filePath = strings.TrimSpace(storageCfg.FilePath)
	}

	appendedRows := csvRowCount(csvData)

	if storeNodeID != "" {
		var storeNode *StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storeNodeID || ws.StoreNodes[i].CanvasNodeID == storeNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}
		if storeNode == nil {
			orihttp.BadRequest(w, "Store node not found")
			return
		}
		storeFilePath := filePath
		if storeFilePath == "" {
			storeFilePath = AppendCSVFileName(task, storageCfg)
		}
		nodeCopy := *storeNode
		nodeCopy.WriteMode = "append"
		nodeCopy.Format = "csv"
		payload, err := CSVWithoutHeaderForExistingStoreStrictInWorkspace(storeNode, h.store, ws.ID, storeFilePath, csvData)
		if err != nil {
			payload = csvWithoutHeader(csvData)
		}
		if err := WriteToStoreForWorkspace(&nodeCopy, h.store, ws.ID, storeFilePath, payload); err != nil {
			logger.Error("Failed to append task result to store node", logger.Fields{
				"task_id":       task.ID,
				"store_node_id": storeNode.ID,
				"err":           err,
			})
			orihttp.InternalError(w, fmt.Sprintf("Append to store node failed: %v", err))
			return
		}
		storeNode.LastWriteTime = nodeCopy.LastWriteTime
		storeNode.WriteCount = nodeCopy.WriteCount
		storeNode.LastFilePath = nodeCopy.LastFilePath
		storeNode.LastError = nodeCopy.LastError
		storeNode.UpdatedAt = nodeCopy.UpdatedAt
		if err := h.store.Save(ws); err != nil {
			logger.Error("Failed to persist workspace after manual append", logger.Fields{"workspace_id": ws.ID, "err": err})
		}
		resolved, _ := BuildFinalStorePath(storeNode, h.store, ws.ID, storeFilePath)
		orihttp.WriteJSON(w, map[string]any{
			"appended_rows": appendedRows,
			"file_path":     resolved,
			"label":         filepath.Base(storeFilePath),
		})
		return
	}

	if req.UseStorage && ResultStorageUsesWorkspaceFolder(storageCfg) {
		baseDir, _, err := ResolveWorkspaceFolderBaseDir(h.store, ws.ID, storageCfg.WorkspaceFolder)
		if err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Invalid workspace folder storage target: %v", err))
			return
		}
		relativeFilePath := filePath
		if relativeFilePath == "" {
			relativeFilePath = AppendCSVFileName(task, storageCfg)
		} else if strings.HasSuffix(relativeFilePath, "/") || !strings.Contains(filepath.Base(relativeFilePath), ".") {
			relativeFilePath = filepath.Join(relativeFilePath, AppendCSVFileName(task, storageCfg))
		}
		finalPath, err := BuildFinalPath(baseDir, relativeFilePath)
		if err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Invalid workspace folder file path: %v", err))
			return
		}
		filePath = finalPath
	} else if filePath == "" {
		baseOutputDir := h.store.GetOutputsPath(ws.ID)
		if baseOutputDir == "" {
			fallback, dirErr := platform.GetDefaultOutputDir()
			if dirErr != nil {
				fallback = "outputs"
			}
			baseOutputDir = filepath.Join(fallback, ws.Name)
		}
		filePath = filepath.Join(baseOutputDir, AppendCSVFileName(task, storageCfg))
	} else if strings.HasSuffix(filePath, "/") || !strings.Contains(filepath.Base(filePath), ".") {
		filePath = filepath.Join(filePath, AppendCSVFileName(task, storageCfg))
	}

	if err := AppendCSVToFile(filePath, csvData); err != nil {
		logger.Error("Failed to append task result CSV to file", logger.Fields{
			"task_id":   task.ID,
			"file_path": filePath,
			"err":       err,
		})
		orihttp.InternalError(w, fmt.Sprintf("Append failed: %v", err))
		return
	}

	orihttp.WriteJSON(w, map[string]any{
		"appended_rows": appendedRows,
		"file_path":     filePath,
		"label":         filepath.Base(filePath),
	})
}

// csvRowCount counts data rows in a CSV string, excluding the header.
func csvRowCount(csvData string) int {
	normalized := strings.TrimSpace(strings.ReplaceAll(csvData, "\r\n", "\n"))
	if normalized == "" {
		return 0
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) <= 1 {
		return 0
	}
	count := 0
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// defaultAppendCSVFilename derives the filename used when the caller did not
// specify a target path. Mirrors the slug rules in autoStoreTaskResult so that
// a one-shot manual append lands in the same place as the scheduled append.
func defaultAppendCSVFilename(task *Task) string {
	name := ""
	if task != nil {
		name = task.Description
	}
	if len(name) > 30 {
		name = name[:30]
	}
	sanitized := strings.Builder{}
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			sanitized.WriteRune(r)
		case r == ' ':
			sanitized.WriteByte('_')
		}
	}
	slug := sanitized.String()
	if slug == "" {
		slug = "task"
	}
	return slug + ".csv"
}
