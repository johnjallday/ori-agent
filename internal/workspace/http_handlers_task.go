package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	ReferenceURL           string               `json:"reference_url"`
	From                   string               `json:"from"`
	To                     string               `json:"to"`
	Tags                   []string             `json:"tags,omitempty"`
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
	referenceURL, err := NormalizeReferenceURL(req.ReferenceURL)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	tags, err := ValidateWorkspaceTags(req.Tags)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	logger.Debug("CreateTask - Workspace and agents", logger.Fields{"workspace_id": workspaceID, "agents": workspace.AgentNames()})
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
		Description:            req.Description,
		ReferenceURL:           referenceURL,
		Tags:                   tags,
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

	// The workspace task HTTP API is the manual (user-driven) path. An explicit
	// assignee is stamped manual; an omitted (empty/"unassigned") assignee
	// defaults to the workspace coordinator (entry agent) when one can be
	// resolved, otherwise it stays unassigned to be claimed later. Membership is
	// validated by the shared assignment service — assigning to a non-member
	// workspace agent is rejected.
	if taskAssigneeIsDefaultable(req.To) {
		workspace.ApplyEntryAgentDefault(&task)
	} else if err := workspace.ApplyTaskAssignment(&task, TaskAssignment{
		AgentName:  req.To,
		Mode:       TaskAssignmentModeManual,
		AssignedBy: TaskAssignedByManual,
	}); err != nil {
		if errors.Is(err, ErrAssigneeNotInWorkspace) {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to assign task: %v", err))
		return
	}

	// Add task to workspace
	if err := workspace.AddTask(task); err != nil {
		logger.Error("CreateTask - AddTask failed", logger.Fields{"task_id": task.ID, "error": err})
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
		ReferenceURL           *string              `json:"reference_url,omitempty"`
		Tags                   *[]string            `json:"tags,omitempty"`
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
	var updatedTask *Task
	for i, task := range workspace.Tasks {
		if task.ID == taskID {
			// Update only provided fields (allowing explicit empty values)
			if req.Description != nil {
				workspace.Tasks[i].Description = *req.Description
			}
			if req.Details != nil {
				workspace.Tasks[i].Details = *req.Details
			}
			if req.ReferenceURL != nil {
				referenceURL, err := NormalizeReferenceURL(*req.ReferenceURL)
				if err != nil {
					orihttp.BadRequest(w, err.Error())
					return
				}
				workspace.Tasks[i].ReferenceURL = referenceURL
			}
			if req.Tags != nil {
				tags, err := ValidateWorkspaceTags(*req.Tags)
				if err != nil {
					orihttp.BadRequest(w, err.Error())
					return
				}
				workspace.Tasks[i].Tags = tags
			}
			if req.From != nil {
				workspace.Tasks[i].From = *req.From
			}
			// Reassignment through the HTTP API is a manual override: stamp manual
			// provenance and validate membership through the shared service.
			if req.To != nil || req.AssignedNodeID != nil {
				newTo := workspace.Tasks[i].To
				if req.To != nil {
					newTo = *req.To
				}
				newNode := workspace.Tasks[i].AssignedNodeID
				if req.AssignedNodeID != nil {
					newNode = *req.AssignedNodeID
				}
				if err := workspace.ApplyTaskAssignment(&workspace.Tasks[i], TaskAssignment{
					AgentName:  newTo,
					NodeID:     newNode,
					Mode:       TaskAssignmentModeManual,
					AssignedBy: TaskAssignedByManual,
				}); err != nil {
					if errors.Is(err, ErrAssigneeNotInWorkspace) {
						orihttp.BadRequest(w, err.Error())
						return
					}
					orihttp.InternalError(w, fmt.Sprintf("Failed to reassign task: %v", err))
					return
				}
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
			updatedTask = &workspace.Tasks[i]
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
		"task":      updatedTask,
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

	logger.Debug("Deleted task from workspace", logger.Fields{"task_id": taskID, "workspace_id": workspaceID})

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

	logger.Debug("Manually executing task in workspace", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})

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

	// Derive the dataset filename from the storage OWNER (the workflow's last
	// subtask when the task has subtasks), not the URL task — that's where the
	// executor writes and where export reads, so a description-derived filename
	// must resolve identically across all three paths.
	var storageCfg *ResultStorageConfig
	storageOwner := task
	if owner := ResolveTaskResultStorageOwner(ws, task); owner != nil {
		storageOwner = owner
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

	// The dataset is JSONL; convert the CSV payload to JSONL records at the
	// append boundary so this path writes the same .jsonl the executor does.
	jsonlData, convErr := CSVToJSONL(csvData)
	if convErr != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Could not parse rows to append: %v", convErr))
		return
	}

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
			storeFilePath = AppendJSONLFileName(storageOwner, storageCfg)
		}
		nodeCopy := *storeNode
		nodeCopy.WriteMode = "append"
		nodeCopy.Format = "jsonl"
		if err := WriteToStoreForWorkspace(&nodeCopy, h.store, ws.ID, storeFilePath, jsonlData); err != nil {
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
			relativeFilePath = AppendJSONLFileName(storageOwner, storageCfg)
		} else if strings.HasSuffix(relativeFilePath, "/") || !strings.Contains(filepath.Base(relativeFilePath), ".") {
			relativeFilePath = filepath.Join(relativeFilePath, AppendJSONLFileName(storageOwner, storageCfg))
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
		filePath = filepath.Join(baseOutputDir, AppendJSONLFileName(storageOwner, storageCfg))
	} else if strings.HasSuffix(filePath, "/") || !strings.Contains(filepath.Base(filePath), ".") {
		filePath = filepath.Join(filePath, AppendJSONLFileName(storageOwner, storageCfg))
	}

	if err := AppendJSONLToFile(filePath, jsonlData); err != nil {
		logger.Error("Failed to append task result to JSONL file", logger.Fields{
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

// ExportResultCSV handles GET /api/workspaces/:id/tasks/:task_id/results/export-csv.
// The canonical append dataset is JSONL; this derives a spreadsheet-friendly CSV
// from it on demand (data columns first, run metadata after) and returns it as a
// download. The .jsonl file on disk is never modified.
func (h *HTTPHandler) ExportResultCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// URL: /api/workspaces/{workspace_id}/tasks/{task_id}/results/export-csv
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

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

	owner := ResolveTaskResultStorageOwner(ws, task)
	if owner == nil {
		owner = task
	}
	storage := owner.ResultStorage
	if storage == nil || !storage.Enabled || !strings.EqualFold(strings.TrimSpace(storage.WriteMode), "append") {
		orihttp.BadRequest(w, "Task does not have an appended dataset to export")
		return
	}

	filePath, err := h.resolveTaskResultJSONLPath(ws, owner, storage)
	if err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Could not resolve dataset file: %v", err))
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			orihttp.NotFound(w, "No dataset has been written yet")
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Read dataset failed: %v", err))
		return
	}

	csvData, err := ExportCSVFromJSONL(string(content), taskExportPreferredColumns(owner))
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Export failed: %v", err))
		return
	}

	downloadName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath)) + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	_, _ = w.Write([]byte(csvData))
}

// resolveTaskResultJSONLPath resolves the local .jsonl file a task's append
// dataset is written to, mirroring the destination resolution the executor and
// manual-append handler use (store node, workspace folder, explicit path, or
// the workspace's default output folder).
func (h *HTTPHandler) resolveTaskResultJSONLPath(ws *Workspace, owner *Task, storage *ResultStorageConfig) (string, error) {
	explicit := strings.TrimSpace(storage.FilePath)
	jsonlName := AppendJSONLFileName(owner, storage)

	if strings.TrimSpace(storage.StoreNodeID) != "" {
		var storeNode *StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storage.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == storage.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}
		if storeNode == nil {
			return "", fmt.Errorf("store node not found")
		}
		storeFilePath := explicit
		if storeFilePath == "" {
			storeFilePath = jsonlName
		}
		return BuildFinalStorePath(storeNode, h.store, ws.ID, storeFilePath)
	}

	if ResultStorageUsesWorkspaceFolder(storage) {
		baseDir, _, err := ResolveWorkspaceFolderBaseDir(h.store, ws.ID, storage.WorkspaceFolder)
		if err != nil {
			return "", err
		}
		relativeFilePath := explicit
		if relativeFilePath == "" {
			relativeFilePath = jsonlName
		} else if strings.HasSuffix(relativeFilePath, "/") || !strings.Contains(filepath.Base(relativeFilePath), ".") {
			relativeFilePath = filepath.Join(relativeFilePath, jsonlName)
		}
		return BuildFinalPath(baseDir, relativeFilePath)
	}

	if explicit != "" {
		if strings.HasSuffix(explicit, "/") || !strings.Contains(filepath.Base(explicit), ".") {
			return filepath.Join(explicit, jsonlName), nil
		}
		return explicit, nil
	}

	baseOutputDir := h.store.GetOutputsPath(ws.ID)
	if baseOutputDir == "" {
		fallback, err := platform.GetDefaultOutputDir()
		if err != nil {
			fallback = "outputs"
		}
		baseOutputDir = filepath.Join(fallback, ws.Name)
	}
	return filepath.Join(baseOutputDir, jsonlName), nil
}

// taskExportPreferredColumns returns the task's declared output columns in
// order, so the exported CSV leads with the data the task produces and trails
// the run metadata.
func taskExportPreferredColumns(task *Task) []string {
	if task == nil {
		return nil
	}
	appendName := func(cols []string, name string) []string {
		if name = strings.TrimSpace(name); name != "" {
			return append(cols, name)
		}
		return cols
	}
	if task.OutputSpec != nil {
		if task.OutputSpec.Schema != nil && len(task.OutputSpec.Schema.Fields) > 0 {
			cols := make([]string, 0, len(task.OutputSpec.Schema.Fields))
			for _, field := range task.OutputSpec.Schema.Fields {
				cols = appendName(cols, field.Name)
			}
			return cols
		}
		if task.OutputSpec.Contract != nil && len(task.OutputSpec.Contract.Columns) > 0 {
			cols := make([]string, 0, len(task.OutputSpec.Contract.Columns))
			for _, column := range task.OutputSpec.Contract.Columns {
				cols = appendName(cols, column.Name)
			}
			return cols
		}
	}
	if task.OutputContract != nil && len(task.OutputContract.Columns) > 0 {
		cols := make([]string, 0, len(task.OutputContract.Columns))
		for _, column := range task.OutputContract.Columns {
			cols = appendName(cols, column.Name)
		}
		return cols
	}
	return nil
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
