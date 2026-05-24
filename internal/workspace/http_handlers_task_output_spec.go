package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type taskOutputSpecDraftRequest struct {
	OutputSpec *TaskOutputSpec `json:"output_spec"`
	Overwrite  bool            `json:"overwrite,omitempty"`
}

func parseTaskOutputSpecPath(path string) (workspaceID, taskID string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/api/workspaces/")
	if trimmed == path {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 4 || parts[1] != "tasks" || parts[3] != "output-spec" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[2]), true
}

func normalizeDraftOutputSpecForRequest(spec *TaskOutputSpec) (*TaskOutputSpec, []string) {
	if spec != nil && strings.TrimSpace(spec.Source) == "" {
		clone := SnapshotTaskOutputSpec(spec)
		if clone == nil {
			clone = spec
		}
		clone.Source = "manual"
		spec = clone
	}
	normalized, errs := NormalizeTaskOutputSpec(spec)
	if normalized != nil {
		normalized.Version = ""
		normalized.Approval = nil
	}
	return normalized, errs
}

// SaveTaskOutputSpecDraft handles POST/PATCH /api/workspaces/{id}/tasks/{task_id}/output-spec/draft.
func (h *HTTPHandler) SaveTaskOutputSpecDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}
	workspaceID, taskID, ok := parseTaskOutputSpecPath(r.URL.Path)
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	var req taskOutputSpecDraftRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	draft, errs := normalizeDraftOutputSpecForRequest(req.OutputSpec)
	if draft == nil {
		if len(errs) == 0 {
			errs = append(errs, "output_spec is required")
		}
		orihttp.BadRequest(w, "Invalid output_spec: "+strings.Join(errs, "; "))
		return
	}
	if len(errs) > 0 {
		orihttp.BadRequest(w, "Invalid output_spec: "+strings.Join(errs, "; "))
		return
	}

	var updated Task
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	if err := MutateTaskAndSave(h.store, ws, taskID, func(task *Task) error {
		if task.DraftOutputSpec != nil && !req.Overwrite {
			return errTaskOutputSpecDraftConflict
		}
		task.DraftOutputSpec = draft
		updated = *task
		return nil
	}); err != nil {
		if err == errTaskOutputSpecDraftConflict {
			orihttp.Conflict(w, "A draft output spec already exists. Set overwrite=true to replace it.")
			return
		}
		orihttp.NotFound(w, err.Error())
		return
	}
	h.publishTaskOutputSpecEvent(workspaceID, taskID, "draft_saved", draft)
	writeTaskOutputSpecResponse(w, updated)
}

var errTaskOutputSpecDraftConflict = fmt.Errorf("draft output spec already exists")

// ApproveTaskOutputSpecDraft handles POST /api/workspaces/{id}/tasks/{task_id}/output-spec/approve.
func (h *HTTPHandler) ApproveTaskOutputSpecDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	workspaceID, taskID, ok := parseTaskOutputSpecPath(r.URL.Path)
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	var updated Task
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	if err := MutateTaskAndSave(h.store, ws, taskID, func(task *Task) error {
		if task.DraftOutputSpec == nil {
			return fmt.Errorf("no draft output spec exists")
		}
		active, errs := NormalizeTaskOutputSpec(task.DraftOutputSpec)
		if active == nil || len(errs) > 0 {
			if len(errs) == 0 {
				errs = append(errs, "draft output spec is invalid")
			}
			return fmt.Errorf("invalid draft output spec: %s", strings.Join(errs, "; "))
		}
		active = AssignTaskOutputSpecVersion(active)
		active.Approval = &TaskOutputApproval{ApprovedAt: time.Now().UTC()}
		task.OutputSpec = active
		task.OutputSchema = active.Schema
		task.OutputContract = active.Contract
		task.DraftOutputSpec = nil
		updated = *task
		return nil
	}); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	h.publishTaskOutputSpecEvent(workspaceID, taskID, "draft_approved", updated.OutputSpec)
	writeTaskOutputSpecResponse(w, updated)
}

// DiscardTaskOutputSpecDraft handles POST/DELETE /api/workspaces/{id}/tasks/{task_id}/output-spec/discard.
func (h *HTTPHandler) DiscardTaskOutputSpecDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}
	workspaceID, taskID, ok := parseTaskOutputSpecPath(r.URL.Path)
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	var updated Task
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	if err := MutateTaskAndSave(h.store, ws, taskID, func(task *Task) error {
		task.DraftOutputSpec = nil
		updated = *task
		return nil
	}); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	h.publishTaskOutputSpecEvent(workspaceID, taskID, "draft_discarded", nil)
	writeTaskOutputSpecResponse(w, updated)
}

func (h *HTTPHandler) publishTaskOutputSpecEvent(workspaceID, taskID, action string, spec *TaskOutputSpec) {
	if h.eventBus == nil {
		return
	}
	data := map[string]any{
		"task_id": taskID,
		"action":  action,
	}
	if spec != nil {
		data["source"] = strings.TrimSpace(spec.Source)
		data["contract_version"] = strings.TrimSpace(spec.Version)
		if spec.Contract != nil {
			data["column_count"] = len(spec.Contract.Columns)
		}
	}
	h.eventBus.Publish(NewWorkspaceEvent(EventTaskOutput, workspaceID, "task.output_spec", data))
}

func writeTaskOutputSpecResponse(w http.ResponseWriter, task Task) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success":           true,
		"task_id":           task.ID,
		"output_spec":       task.OutputSpec,
		"draft_output_spec": task.DraftOutputSpec,
		"output_schema":     task.OutputSchema,
		"output_contract":   task.OutputContract,
		"result_storage":    task.ResultStorage,
		"task":              task,
	}); err != nil {
		logger.Error("Failed to encode task output spec response", logger.Fields{"error": err})
	}
}
