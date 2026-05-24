package orchestrationhttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workflowSubtaskInput captures the per-step fields a workflow create call
// understands. It mirrors the subset of taskCreateRequest the modal builder
// actually uses; if you need to surface a new top-level Task field through
// this endpoint, add it here AND in materializeWorkflowTask below — the
// handler intentionally rejects unknown fields rather than silently
// ignoring them so the contract stays explicit.
type workflowSubtaskInput struct {
	ID             string                         `json:"id"`
	Description    string                         `json:"description"`
	Details        string                         `json:"details"`
	To             string                         `json:"to"`
	AssignedNodeID string                         `json:"assigned_node_id"`
	Priority       int                            `json:"priority"`
	InputTaskIDs   []string                       `json:"input_task_ids"`
	SubtaskIndex   int                            `json:"subtask_index"`
	ResultStorage  *workspace.ResultStorageConfig `json:"result_storage"`
	OutputContract *workspace.TaskOutputContract  `json:"output_contract"`
}

type workflowParentInput struct {
	ID             string                         `json:"id"`
	Description    string                         `json:"description"`
	Details        string                         `json:"details"`
	From           string                         `json:"from"`
	To             string                         `json:"to"`
	AssignedNodeID string                         `json:"assigned_node_id"`
	Priority       int                            `json:"priority"`
	InputTaskIDs   []string                       `json:"input_task_ids"`
	ResultStorage  *workspace.ResultStorageConfig `json:"result_storage"`
	OutputContract *workspace.TaskOutputContract  `json:"output_contract"`
	ParentTaskID   string                         `json:"parent_task_id"`
}

type workflowCreateRequest struct {
	WorkspaceID string                 `json:"workspace_id"`
	Parent      *workflowParentInput   `json:"parent"`
	Subtasks    []workflowSubtaskInput `json:"subtasks"`
	// AttachToParentID, when set, skips parent creation and attaches the
	// supplied subtasks underneath an existing task. Used by the
	// "Break this into steps" flow on the task detail page, which already
	// has a real parent and just wants to add subtasks atomically. Either
	// `parent` or `attach_to_parent_id` must be present, not both.
	AttachToParentID string `json:"attach_to_parent_id"`
}

// HandleCreateWorkflow accepts a parent task plus N subtasks and persists
// them atomically. Subtasks may reference each other through input_task_ids
// using the IDs the client generates and supplies in the same payload —
// AddTasks validates the resulting graph in one pass, so there's no
// half-created workflow if the user wired up a cycle or a dangling input.
//
// Expected route: POST /api/orchestration/workflows
func (th *TaskHandler) HandleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req workflowCreateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.WorkspaceID) == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	attachID := strings.TrimSpace(req.AttachToParentID)
	if attachID != "" && req.Parent != nil {
		orihttp.BadRequest(w, "send either parent or attach_to_parent_id, not both")
		return
	}
	if attachID == "" && req.Parent == nil {
		orihttp.BadRequest(w, "parent or attach_to_parent_id is required")
		return
	}
	if req.Parent != nil && strings.TrimSpace(req.Parent.Description) == "" {
		orihttp.BadRequest(w, "parent description is required")
		return
	}
	if len(req.Subtasks) == 0 {
		orihttp.BadRequest(w, "at least one subtask is required")
		return
	}

	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"error": err, "workspace_id": req.WorkspaceID})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}

	// In attach-mode the parent must already exist in the workspace.
	// Check up front so we can surface a clean error rather than having
	// AddTasks reject the whole batch with a generic "unknown parent"
	// graph issue.
	parentTaskID := attachID
	if attachID != "" {
		if _, err := ws.GetTask(attachID); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "attach_to_parent_id does not match an existing task", err)
			return
		}
	}

	now := time.Now()

	// Auto-add referenced agents to the workspace ahead of the batch insert
	// so AddTasks doesn't refuse subtasks whose `to` field references an
	// agent the workspace has never seen. This mirrors the convenience
	// already provided by the single-task create endpoint.
	if req.Parent != nil {
		if assignee := strings.TrimSpace(req.Parent.To); assignee != "" && assignee != "unassigned" && !ws.HasAgent(assignee) {
			if err := ws.AddAgent(assignee); err != nil {
				logger.Warn("Failed to auto-add agent to workspace", logger.Fields{"agent": assignee, "error": err})
			}
		}
	}
	for _, sub := range req.Subtasks {
		assignee := strings.TrimSpace(sub.To)
		if assignee == "" || assignee == "unassigned" {
			continue
		}
		if !ws.HasAgent(assignee) {
			if err := ws.AddAgent(assignee); err != nil {
				logger.Warn("Failed to auto-add agent to workspace", logger.Fields{"agent": assignee, "error": err})
			}
		}
	}

	tasks := make([]workspace.Task, 0, 1+len(req.Subtasks))

	// Create-mode appends a new parent task. Attach-mode skips this and
	// just builds the subtask batch under an existing parent ID.
	if req.Parent != nil {
		if strings.TrimSpace(req.Parent.ID) == "" {
			orihttp.BadRequest(w, "parent.id is required so subtasks can reference it")
			return
		}
		parentTaskID = strings.TrimSpace(req.Parent.ID)
		tasks = append(tasks, workspace.Task{
			ID:             parentTaskID,
			WorkspaceID:    req.WorkspaceID,
			From:           req.Parent.From,
			To:             req.Parent.To,
			AssignedNodeID: req.Parent.AssignedNodeID,
			Description:    req.Parent.Description,
			Details:        req.Parent.Details,
			Priority:       normalizeTaskPriority(req.Parent.Priority),
			InputTaskIDs:   req.Parent.InputTaskIDs,
			ParentTaskID:   strings.TrimSpace(req.Parent.ParentTaskID),
			Status:         workspace.TaskStatusPending,
			ResultStorage:  req.Parent.ResultStorage,
			OutputContract: workspace.NormalizeTaskOutputContract(req.Parent.OutputContract),
			CreatedAt:      now,
		})
	}

	for i, sub := range req.Subtasks {
		if strings.TrimSpace(sub.Description) == "" {
			orihttp.BadRequest(w, "subtask description is required")
			return
		}
		subtaskIndex := sub.SubtaskIndex
		if subtaskIndex <= 0 {
			subtaskIndex = i + 1
		}
		tasks = append(tasks, workspace.Task{
			ID:             strings.TrimSpace(sub.ID),
			WorkspaceID:    req.WorkspaceID,
			To:             sub.To,
			AssignedNodeID: sub.AssignedNodeID,
			Description:    sub.Description,
			Details:        sub.Details,
			Priority:       normalizeTaskPriority(sub.Priority),
			InputTaskIDs:   sub.InputTaskIDs,
			ParentTaskID:   parentTaskID,
			SubtaskIndex:   subtaskIndex,
			Status:         workspace.TaskStatusPending,
			ResultStorage:  sub.ResultStorage,
			OutputContract: workspace.NormalizeTaskOutputContract(sub.OutputContract),
			CreatedAt:      now,
		})
	}

	if err := ws.AddTasks(tasks); err != nil {
		if respondTaskGraphError(w, err, "Failed to create workflow") {
			return
		}
		logger.Error("Failed to add workflow tasks", logger.Fields{"error": err, "workspace_id": req.WorkspaceID})
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to create workflow", err)
		return
	}

	if err := th.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace after workflow create", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	if th.eventBus != nil {
		th.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventWorkspaceUpdated, req.WorkspaceID, "workflow.create", map[string]any{
			"parent_task_id": parentTaskID,
			"subtask_count":  len(req.Subtasks),
			"attached":       attachID != "",
		}))
		// Surface a TaskCreated event for the parent only when we created
		// it. In attach-mode we're adding subtasks under an existing task
		// and the parent already had its own TaskCreated event when it
		// was first created.
		if req.Parent != nil {
			th.eventBus.Publish(workspace.Event{
				Type:        workspace.EventTaskCreated,
				WorkspaceID: req.WorkspaceID,
				Source:      "api",
				Data: map[string]any{
					"task_id":     parentTaskID,
					"description": req.Parent.Description,
					"to":          req.Parent.To,
					"status":      workspace.TaskStatusPending,
				},
			})
		}
	}

	logger.Info("Created workflow in workspace", logger.Fields{
		"workspace_id":   req.WorkspaceID,
		"parent_task_id": parentTaskID,
		"subtask_count":  len(req.Subtasks),
		"attached":       attachID != "",
	})

	// Re-read the persisted parent + subtasks from the workspace so the
	// response carries the canonical timestamps and assigned IDs (the
	// caller already knows the UUIDs it sent, but the modal currently
	// expects to see the server's notion of `created_at` etc.).
	createdParent, _ := ws.GetTask(parentTaskID)
	createdSubtasks := make([]*workspace.Task, 0, len(req.Subtasks))
	for _, sub := range req.Subtasks {
		if sub.ID == "" {
			continue
		}
		if t, err := ws.GetTask(sub.ID); err == nil {
			createdSubtasks = append(createdSubtasks, t)
		}
	}

	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, map[string]any{
		"success":  true,
		"parent":   createdParent,
		"subtasks": createdSubtasks,
	})
}

// respondTaskGraphError checks whether err is a *workspace.TaskGraphError;
// if so, it writes a structured 400 with the issue list and returns true.
// Returns false (and writes nothing) for any other error type, so callers
// can fall through to their own error path.
//
// The response shape is:
//
//	{
//	  "success": false,
//	  "error":   "<message>: <flat issue summary>",
//	  "issues": [ {"kind":..., "task_id":..., "reference":..., "message":...}, ... ]
//	}
//
// `error` carries the same flat string the legacy clients expect, so older
// surfaces keep working. New surfaces (the modal subtask highlighter) use
// `issues` for per-row feedback.
func respondTaskGraphError(w http.ResponseWriter, err error, message string) bool {
	var graphErr *workspace.TaskGraphError
	if !errors.As(err, &graphErr) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	orihttp.WriteJSON(w, map[string]any{
		"success": false,
		"error":   message + ": " + graphErr.Error(),
		"issues":  graphErr.Issues,
	})
	return true
}
