package orchestrationhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// handleSaveTaskResult handles POST /api/orchestration/tasks/{id}/save-result
func (th *TaskHandler) handleSaveTaskResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req SaveTaskResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "Invalid request body")
		return
	}

	// Extract task ID from URL if not in body
	if req.TaskID == "" {
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
		if len(pathParts) >= 1 && pathParts[0] != "" {
			req.TaskID = pathParts[0]
		}
	}

	if req.TaskID == "" {
		orihttp.BadRequest(w, "Task ID is required")
		return
	}
	if req.FilePath == "" {
		orihttp.BadRequest(w, "File path is required")
		return
	}

	// Security: Validate file path to prevent path traversal attacks
	cleanFilePath := filepath.Clean(req.FilePath)
	if strings.Contains(cleanFilePath, "..") {
		orihttp.BadRequest(w, "Invalid file path: path traversal not allowed")
		return
	}
	req.FilePath = cleanFilePath

	// Set default format
	if req.Format == "" {
		req.Format = "text"
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "text": true, "markdown": true, "csv": true}
	if !validFormats[req.Format] {
		orihttp.BadRequest(w, "Format must be one of: json, text, markdown, csv")
		return
	}

	// Find the task
	task, ws, err := th.getTaskWithWorkspace(req.TaskID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Task not found: %v", err))
		return
	}

	if task.Result == "" {
		orihttp.BadRequest(w, "Task has no result to save")
		return
	}

	var finalPath string

	if req.StoreNodeID != "" {
		// Save via store node
		var storeNode *workspace.StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == req.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == req.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}

		if storeNode == nil {
			orihttp.NotFound(w, "Store node not found")
			return
		}

		// Override format with store node's format
		storeNode.Format = req.Format

		dataToStore := task.Result
		if req.Format == "csv" {
			dataToStore = workspace.TaskResultToCSV(task, task.Result, time.Now().Format("20060102-150405"), "")
		}

		// Write to store
		if err := workspace.WriteToStoreForWorkspace(storeNode, th.workspaceStore, ws.ID, req.FilePath, dataToStore); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to save result: %v", err))
			return
		}

		finalPath, _ = workspace.BuildFinalStorePath(storeNode, th.workspaceStore, ws.ID, req.FilePath)

		// Save workspace to persist store node stats
		if err := th.workspaceStore.Save(ws); err != nil {
			logger.Warn("Failed to save workspace after store write", logger.Fields{"error": err})
		}
	} else {
		// Direct file save (for Quick Save or custom path)
		// Format data based on format type
		var formattedData []byte
		switch req.Format {
		case "json":
			// Pretty-print JSON
			var obj any
			if err := json.Unmarshal([]byte(task.Result), &obj); err != nil {
				// If not valid JSON, treat as plain text
				formattedData = []byte(task.Result)
			} else {
				formattedData, _ = json.MarshalIndent(obj, "", "  ")
			}
		case "csv":
			formattedData = []byte(workspace.TaskResultToCSV(task, task.Result, time.Now().Format("20060102-150405"), ""))
		default:
			formattedData = []byte(task.Result)
		}

		// Create directories
		dir := filepath.Dir(req.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil { // #nosec G301 -- preserves the 0755 permissions used for user-facing task output directories prior to this refactor
			orihttp.InternalError(w, fmt.Sprintf("Failed to create directories: %v", err))
			return
		}

		// Write file
		if err := os.WriteFile(req.FilePath, formattedData, 0644); err != nil { // #nosec G306 -- preserves the 0644 permissions used for user-facing task output files prior to this refactor
			orihttp.InternalError(w, fmt.Sprintf("Failed to write file: %v", err))
			return
		}

		finalPath = req.FilePath
	}

	logger.Info("Saved task result", logger.Fields{
		"task_id":   req.TaskID,
		"file_path": finalPath,
		"format":    req.Format,
	})

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"message":   "Result saved successfully",
		"file_path": finalPath,
		"task_id":   req.TaskID,
	})
}

func (th *TaskHandler) handleTaskOutputReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) < 2 || pathParts[0] == "" {
		orihttp.BadRequest(w, "task_id is required in URL path")
		return
	}
	taskID := pathParts[0]

	var req taskOutputReviewRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		orihttp.BadRequest(w, "action is required")
		return
	}

	task, ws, err := th.getTaskWithWorkspace(taskID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Task not found", err)
		return
	}
	historyIndex := resolveTaskReviewHistoryIndex(task, req.HistoryIndex)
	if historyIndex < 0 || historyIndex >= len(task.ExecutionHistory) {
		orihttp.BadRequest(w, "history_index does not identify a reviewable run")
		return
	}

	// Set by the cases that attempt to store a row so the shared response
	// below can report whether the row was actually written. A CSV header
	// mismatch holds the row for review (StorageStatus=skipped_invalid) while
	// the action itself succeeds, so "success" alone is not enough for a
	// non-UI client to tell that nothing was appended.
	var reviewValidation *workspace.TaskValidationResult

	switch action {
	case "inspect", "copy_raw":
		th.publishOutputContractReviewEvent(ws.ID, task.ID, action, task.ExecutionHistory[historyIndex].Validation)
		entry := task.ExecutionHistory[historyIndex]
		w.WriteHeader(http.StatusOK)
		orihttp.WriteJSON(w, map[string]any{
			"success":           true,
			"task_id":           task.ID,
			"history_index":     historyIndex,
			"result":            entry.Result,
			"summary":           entry.Summary,
			"validation_result": entry.Validation,
		})
		return
	case "retry_normalization":
		entry := task.ExecutionHistory[historyIndex]
		rawResult := strings.TrimSpace(entry.Result)
		if rawResult == "" {
			rawResult = strings.TrimSpace(entry.Summary)
		}
		if rawResult == "" {
			rawResult = strings.TrimSpace(task.Result)
		}
		if rawResult == "" {
			orihttp.BadRequest(w, "raw result is required to retry normalization")
			return
		}
		reviewTask := taskOutputReviewValidationTaskForEntry(task, entry)
		if reviewTask == nil || reviewTask.OutputSpec == nil {
			orihttp.BadRequest(w, "retry_normalization requires a structured output spec snapshot")
			return
		}
		var assistant workspace.TaskOutputSpecAssistant
		if candidate, ok := th.taskHandler.(workspace.TaskOutputSpecAssistant); ok {
			assistant = candidate
		}
		validation, csvData := workspace.ValidateTaskOutputSpecResultWithAssistant(r.Context(), reviewTask, rawResult, assistant)
		if validation.ValidationStatus == workspace.TaskValidationPassed {
			if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
				if !recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
					orihttp.InternalError(w, fmt.Sprintf("Failed to append retried normalization: %v", err))
					return
				}
			} else {
				validation.StorageStatus = workspace.TaskStorageAppended
			}
		}
		now := time.Now().UTC()
		validation.ValidatedAt = &now
		if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record normalization retry: %v", err))
			return
		}
		reviewValidation = validation
	case "rerun":
		if err := th.startTaskOutputReviewRerun(ws.ID, task.ID); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to re-run task", err)
			return
		}
		th.publishOutputContractReviewEvent(ws.ID, task.ID, action, task.ExecutionHistory[historyIndex].Validation)
		w.WriteHeader(http.StatusAccepted)
		orihttp.WriteJSON(w, map[string]any{
			"success": true,
			"message": "Task re-run started",
			"task_id": task.ID,
		})
		return
	case "dismiss":
		now := time.Now().UTC()
		if err := th.workspaceStore.Update(ws.ID, func(fresh *workspace.Workspace) error {
			return fresh.MutateTask(taskID, func(t *workspace.Task) error {
				if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
					return fmt.Errorf("history entry no longer exists")
				}
				validation := t.ExecutionHistory[historyIndex].Validation
				if validation == nil {
					validation = &workspace.TaskValidationResult{}
				}
				validation.ValidationStatus = workspace.TaskValidationDismissed
				if validation.StorageStatus == "" {
					validation.StorageStatus = workspace.TaskStorageSkippedInvalid
				}
				validation.ValidatedAt = &now
				t.ExecutionHistory[historyIndex].Validation = validation
				workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
				th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
				return nil
			})
		}); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to dismiss review: %v", err))
			return
		}
	case "approve_append":
		draft := strings.TrimSpace(req.Result)
		if draft == "" {
			draft = strings.TrimSpace(task.ExecutionHistory[historyIndex].Result)
		}
		if draft == "" {
			orihttp.BadRequest(w, "result is required for manual approval")
			return
		}
		reviewTask := taskOutputReviewValidationTask(task, task.ExecutionHistory[historyIndex].Validation)
		validation, csvData := validateTaskOutputReviewApproval(reviewTask, draft)
		if validation.ValidationStatus != workspace.TaskValidationPassed {
			th.publishOutputContractReviewEvent(ws.ID, task.ID, action, validation)
			w.WriteHeader(http.StatusBadRequest)
			orihttp.WriteJSON(w, map[string]any{
				"success":           false,
				"validation_result": validation,
				"message":           "Edited result does not match the output contract.",
			})
			return
		}
		if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
			if recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
				now := time.Now().UTC()
				validation.ValidatedAt = &now
				if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
					orihttp.InternalError(w, fmt.Sprintf("Failed to record append review: %v", err))
					return
				}
				reviewValidation = validation
				break
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to append approved result: %v", err))
			return
		}
		now := time.Now().UTC()
		validation.ValidationStatus = workspace.TaskValidationManuallyApproved
		validation.StorageStatus = workspace.TaskStorageManuallyAppended
		validation.Errors = nil
		validation.ManualApproval = &workspace.TaskManualApproval{
			ApprovedAt: now,
			ApprovedBy: strings.TrimSpace(req.ApprovedBy),
		}
		validation.ValidatedAt = &now
		if err := th.workspaceStore.Update(ws.ID, func(fresh *workspace.Workspace) error {
			return fresh.MutateTask(taskID, func(t *workspace.Task) error {
				if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
					return fmt.Errorf("history entry no longer exists")
				}
				t.ExecutionHistory[historyIndex].Validation = validation
				workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
				th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
				return nil
			})
		}); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record approval: %v", err))
			return
		}
		reviewValidation = validation
	case "reproject_to_destination":
		entry := task.ExecutionHistory[historyIndex]
		rawResult := strings.TrimSpace(entry.Result)
		if rawResult == "" {
			rawResult = strings.TrimSpace(entry.Summary)
		}
		if rawResult == "" {
			rawResult = strings.TrimSpace(task.Result)
		}
		if rawResult == "" {
			orihttp.BadRequest(w, "raw result is required to reproject")
			return
		}
		targetColumns := req.TargetColumns
		if len(targetColumns) == 0 {
			targetColumns = expectedColumnsFromValidation(entry.Validation)
		}
		if len(targetColumns) == 0 {
			orihttp.BadRequest(w, "no destination columns available to match; provide target_columns")
			return
		}
		var assistant workspace.TaskOutputSpecAssistant
		if candidate, ok := th.taskHandler.(workspace.TaskOutputSpecAssistant); ok {
			assistant = candidate
		}
		csvData, _, reprojErr := workspace.ReprojectResultToColumns(r.Context(), task, rawResult, targetColumns, assistant)
		if reprojErr != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to reorganize result: %v", reprojErr))
			return
		}
		validation := entry.Validation
		if validation == nil {
			validation = &workspace.TaskValidationResult{}
		}
		if err := appendApprovedTaskCSV(th.workspaceStore, ws, task, csvData); err != nil {
			// A mismatch here would be unexpected (we matched the file's header),
			// but surface it for review rather than failing silently.
			if recordTaskOutputReviewCSVHeaderMismatch(validation, err) {
				now := time.Now().UTC()
				validation.ValidatedAt = &now
				if recordErr := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); recordErr != nil {
					orihttp.InternalError(w, fmt.Sprintf("Failed to record reorganize review: %v", recordErr))
					return
				}
				reviewValidation = validation
				break
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to append reorganized result: %v", err))
			return
		}
		now := time.Now().UTC()
		validation.ValidationStatus = workspace.TaskValidationManuallyApproved
		validation.StorageStatus = workspace.TaskStorageManuallyAppended
		validation.Errors = nil
		validation.ManualApproval = &workspace.TaskManualApproval{
			ApprovedAt: now,
			ApprovedBy: strings.TrimSpace(req.ApprovedBy),
		}
		validation.ValidatedAt = &now
		if err := th.recordTaskOutputReviewValidation(ws.ID, taskID, historyIndex, action, validation); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to record reorganized append: %v", err))
			return
		}
		reviewValidation = validation
	default:
		orihttp.BadRequest(w, "action must be inspect, copy_raw, dismiss, rerun, retry_normalization, reproject_to_destination, or approve_append")
		return
	}

	updatedTask, err := th.communicator.GetTask(taskID)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to load updated task: %v", err))
		return
	}
	resp := map[string]any{
		"success": true,
		"task":    updatedTask,
	}
	// When the action attempted to store a row, report whether it actually
	// landed. A CSV header mismatch holds the row for review rather than
	// appending it, so "success" (the action was processed) must not be read
	// as "the row was stored".
	if reviewValidation != nil {
		resp["validation_status"] = reviewValidation.ValidationStatus
		resp["storage_status"] = reviewValidation.StorageStatus
		resp["stored"] = reviewValidation.StorageStatus == workspace.TaskStorageAppended ||
			reviewValidation.StorageStatus == workspace.TaskStorageSaved ||
			reviewValidation.StorageStatus == workspace.TaskStorageManuallyAppended
	}
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, resp)
}

func (th *TaskHandler) publishOutputContractReviewEvent(workspaceID, taskID, action string, validation *workspace.TaskValidationResult) {
	if th.eventBus == nil {
		return
	}
	data := map[string]any{
		"task_id": taskID,
		"action":  "review_action",
		"review":  strings.TrimSpace(action),
	}
	if validation != nil {
		data["validation_status"] = validation.ValidationStatus
		data["storage_status"] = validation.StorageStatus
		data["contract_version"] = validation.ContractVersion
		data["error_count"] = len(validation.Errors)
	}
	th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventTaskOutput, workspaceID, "task.output_contract", data))
}

func (th *TaskHandler) recordTaskOutputReviewValidation(workspaceID, taskID string, historyIndex int, action string, validation *workspace.TaskValidationResult) error {
	return th.workspaceStore.Update(workspaceID, func(fresh *workspace.Workspace) error {
		return fresh.MutateTask(taskID, func(t *workspace.Task) error {
			if historyIndex < 0 || historyIndex >= len(t.ExecutionHistory) {
				return fmt.Errorf("history entry no longer exists")
			}
			t.ExecutionHistory[historyIndex].Validation = validation
			workspace.MirrorTaskValidationResult(fresh.ID, t.ID, t.ExecutionHistory[historyIndex].RunID, validation)
			th.publishOutputContractReviewEvent(fresh.ID, t.ID, action, validation)
			return nil
		})
	})
}

// expectedColumnsFromValidation pulls the destination file's column header out
// of a recorded csv_header_mismatch error, so a reproject can target the exact
// columns the existing CSV expects.
func expectedColumnsFromValidation(validation *workspace.TaskValidationResult) []string {
	if validation == nil {
		return nil
	}
	for _, e := range validation.Errors {
		if strings.EqualFold(strings.TrimSpace(e.Code), "csv_header_mismatch") && len(e.Expected) > 0 {
			return append([]string(nil), e.Expected...)
		}
	}
	return nil
}

func recordTaskOutputReviewCSVHeaderMismatch(validation *workspace.TaskValidationResult, err error) bool {
	var mismatch *workspace.CSVHeaderMismatchError
	if !errors.As(err, &mismatch) {
		return false
	}
	if validation == nil {
		return false
	}
	validation.ValidationStatus = workspace.TaskValidationNeedsReview
	validation.StorageStatus = workspace.TaskStorageSkippedInvalid
	validation.Errors = append(validation.Errors, workspace.TaskValidationError{
		Code:     "csv_header_mismatch",
		Message:  mismatch.Error(),
		Expected: append([]string(nil), mismatch.Expected...),
		Actual:   append([]string(nil), mismatch.Actual...),
	})
	return true
}

func validateTaskOutputReviewApproval(task *workspace.Task, result string) (*workspace.TaskValidationResult, string) {
	if task != nil && task.OutputSpec != nil {
		return workspace.ValidateTaskOutputSpecResult(task, result)
	}
	return workspace.ValidateTaskOutputContractResult(task, result)
}

func taskOutputReviewValidationTask(task *workspace.Task, validation *workspace.TaskValidationResult) *workspace.Task {
	if task == nil {
		return nil
	}
	reviewTask := *task
	if validation != nil && validation.OutputSpec != nil {
		reviewTask.OutputSpec = workspace.SnapshotTaskOutputSpec(validation.OutputSpec)
		reviewTask.OutputSchema = nil
		reviewTask.OutputContract = nil
		if reviewTask.OutputSpec != nil {
			reviewTask.OutputSchema = reviewTask.OutputSpec.Schema
			reviewTask.OutputContract = reviewTask.OutputSpec.Contract
		}
	}
	return &reviewTask
}

func taskOutputReviewValidationTaskForEntry(task *workspace.Task, entry workspace.TaskExecution) *workspace.Task {
	reviewTask := taskOutputReviewValidationTask(task, entry.Validation)
	if reviewTask == nil {
		return nil
	}
	reviewTask.CurrentRunID = strings.TrimSpace(entry.RunID)
	reviewTask.ExecutionHistory = []workspace.TaskExecution{entry}
	if reviewTask.Context == nil {
		reviewTask.Context = map[string]any{}
	}
	return reviewTask
}

func resolveTaskReviewHistoryIndex(task *workspace.Task, requested *int) int {
	if task == nil || len(task.ExecutionHistory) == 0 {
		return -1
	}
	if requested != nil && *requested >= 0 && *requested < len(task.ExecutionHistory) {
		return *requested
	}
	for i := len(task.ExecutionHistory) - 1; i >= 0; i-- {
		validation := task.ExecutionHistory[i].Validation
		if validation != nil && validation.ValidationStatus == workspace.TaskValidationNeedsReview {
			return i
		}
	}
	return -1
}

func (th *TaskHandler) startTaskOutputReviewRerun(workspaceID, taskID string) error {
	if th.taskHandler == nil {
		return fmt.Errorf("task execution not available")
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		return err
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Status == workspace.TaskStatusInProgress {
		return fmt.Errorf("task is already in progress")
	}

	subtasks := ws.GetSubtasks(task.ID)
	if len(subtasks) > 0 {
		for _, subtask := range subtasks {
			if subtask.Status == workspace.TaskStatusInProgress {
				return fmt.Errorf("a subtask is already in progress")
			}
			if subtask.To == "" || subtask.To == "unassigned" {
				return fmt.Errorf("all subtasks must be assigned to an agent before execution")
			}
		}
		go th.executeParentTaskSequence(ws.ID, task.ID)
		return nil
	}

	if err := task.SetStatus(workspace.TaskStatusPending); err != nil {
		return err
	}
	workspace.ResetTaskRuntime(task)
	if err := ws.UpdateTask(*task); err != nil {
		return err
	}
	if err := th.workspaceStore.Save(ws); err != nil {
		return err
	}

	go func() {
		fresh, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to reload workspace for review re-run", logger.Fields{"workspace_id": workspaceID, "error": err})
			return
		}
		rerunTask, err := fresh.GetTask(taskID)
		if err != nil {
			logger.Error("Task not found for review re-run", logger.Fields{"task_id": taskID, "error": err})
			return
		}
		if _, err := th.executeTaskWithDependencies(fresh, rerunTask); err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return
			}
			logger.Error("Review task re-run failed", logger.Fields{"task_id": taskID, "error": err})
		}
	}()
	return nil
}

func appendApprovedTaskCSV(store workspace.Store, ws *workspace.Workspace, task *workspace.Task, csvData string) error {
	if ws == nil || task == nil {
		return fmt.Errorf("workspace and task are required")
	}
	storage := task.ResultStorage
	if storage == nil || !storage.Enabled {
		return fmt.Errorf("task result storage is not enabled")
	}
	if strings.ToLower(strings.TrimSpace(storage.WriteMode)) != "append" {
		return fmt.Errorf("manual approval append requires append-to-CSV storage")
	}
	if strings.TrimSpace(csvData) == "" {
		return fmt.Errorf("approved CSV is empty")
	}

	// The dataset is JSONL; convert the approved CSV to JSONL records and append
	// those so the approve-held-run path writes the same .jsonl as the executor.
	jsonlData, err := workspace.CSVToJSONL(csvData)
	if err != nil {
		return err
	}

	storeFilePath := storage.FilePath
	if strings.TrimSpace(storeFilePath) == "" {
		storeFilePath = workspace.AppendJSONLFileName(task, storage)
	}

	if strings.TrimSpace(storage.StoreNodeID) != "" {
		var storeNode *workspace.StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storage.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == storage.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}
		if storeNode == nil {
			return fmt.Errorf("store node %q not found", storage.StoreNodeID)
		}
		storeNodeCopy := *storeNode
		storeNodeCopy.WriteMode = "append"
		storeNodeCopy.Format = "jsonl"
		// JSONL records are self-describing; append them directly (no CSV
		// header reconciliation).
		if err := workspace.WriteToStoreForWorkspace(&storeNodeCopy, store, ws.ID, storeFilePath, jsonlData); err != nil {
			return err
		}
		storeNode.LastWriteTime = storeNodeCopy.LastWriteTime
		storeNode.WriteCount = storeNodeCopy.WriteCount
		storeNode.LastFilePath = storeNodeCopy.LastFilePath
		storeNode.LastError = storeNodeCopy.LastError
		storeNode.UpdatedAt = storeNodeCopy.UpdatedAt
		return nil
	}

	filePath := storage.FilePath
	if workspace.ResultStorageUsesWorkspaceFolder(storage) {
		baseDir, _, err := workspace.ResolveWorkspaceFolderBaseDir(store, ws.ID, storage.Folder)
		if err != nil {
			return err
		}
		relativeFilePath := strings.TrimSpace(filePath)
		if relativeFilePath == "" {
			relativeFilePath = storeFilePath
		} else if strings.HasSuffix(relativeFilePath, "/") || !strings.Contains(filepath.Base(relativeFilePath), ".") {
			relativeFilePath = filepath.Join(relativeFilePath, workspace.AppendJSONLFileName(task, storage))
		}
		finalPath, err := workspace.BuildFinalPath(baseDir, relativeFilePath)
		if err != nil {
			return err
		}
		filePath = finalPath
	} else if strings.TrimSpace(filePath) == "" {
		baseOutputDir := ""
		if store != nil {
			baseOutputDir = store.GetOutputsPath(ws.ID)
		}
		if baseOutputDir == "" {
			fallback, err := platform.GetDefaultOutputDir()
			if err != nil {
				fallback = "outputs"
			}
			baseOutputDir = filepath.Join(fallback, ws.Name)
		}
		filePath = filepath.Join(baseOutputDir, storeFilePath)
	} else if strings.HasSuffix(filePath, "/") || !strings.Contains(filepath.Base(filePath), ".") {
		filePath = filepath.Join(filePath, workspace.AppendJSONLFileName(task, storage))
	}
	return workspace.AppendJSONLToFile(filePath, jsonlData)
}
