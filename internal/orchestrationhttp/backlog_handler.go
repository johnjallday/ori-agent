package orchestrationhttp

import (
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// BacklogHandler serves the focused Backlog HTTP API (Group 2;
// tasks/prd-workspace-backlog.md FR19-22, 30-31, 46-49) over
// workspace.BacklogService. It is deliberately separate from TaskHandler:
// Backlog is a distinct product surface (Details panel/drawer, Quest Board)
// from executable Tasks, even though both operate on workspace.Task records.
//
// Every route requires an explicit workspace_id naming the item's owning
// workspace — unlike the legacy task-update endpoint, which resolves a task's
// workspace by scanning every workspace, this API never trusts a parent
// roll-up view or global Map link as mutation authority (FR48-50, 60, 63-65):
// a roll-up card always carries its own OwningWorkspaceID, and callers must
// route mutations through that ID.
type BacklogHandler struct {
	service *workspace.BacklogService
}

// NewBacklogHandler constructs a BacklogHandler over the given service.
func NewBacklogHandler(service *workspace.BacklogService) *BacklogHandler {
	return &BacklogHandler{service: service}
}

func (bh *BacklogHandler) itemResponse(task *workspace.Task) workspace.BacklogItemView {
	return workspace.BacklogItemView{
		Task:                *task,
		OwningWorkspaceID:   task.WorkspaceID,
		OwningWorkspaceName: bh.service.WorkspaceName(task.WorkspaceID),
	}
}

// BacklogListHandler handles GET (list) and POST (create) on
// /api/orchestration/backlog.
func (bh *BacklogHandler) BacklogListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		bh.handleList(w, r)
	case http.MethodPost:
		bh.handleCreate(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (bh *BacklogHandler) handleList(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	includeDescendants, _ := strconv.ParseBool(r.URL.Query().Get("include_descendants"))

	items, err := bh.service.List(workspaceID, includeDescendants)
	if err != nil {
		logger.Error("Failed to list backlog items", logger.Fields{"error": err, "workspace_id": workspaceID})
		orihttp.RespondErrorWithErr(w, http.StatusNotFound, "Workspace not found", err)
		return
	}
	if items == nil {
		items = []workspace.BacklogItemView{}
	}
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"items":   items,
		"count":   len(items),
		"sync":    bh.service.SyncStatus(workspaceID),
	})
}

func (bh *BacklogHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID  string   `json:"workspace_id"`
		Description  string   `json:"description"`
		Details      string   `json:"details"`
		Tags         []string `json:"tags"`
		Priority     int      `json:"priority"`
		ReferenceURL string   `json:"reference_url"`
		SourceType   string   `json:"source_type"`
		SourceID     string   `json:"source_id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	task, err := bh.service.Create(workspace.BacklogCreateInput{
		WorkspaceID:  req.WorkspaceID,
		Description:  req.Description,
		Details:      req.Details,
		Tags:         req.Tags,
		Priority:     req.Priority,
		ReferenceURL: req.ReferenceURL,
		SourceType:   req.SourceType,
		SourceID:     req.SourceID,
	})
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"item":    bh.itemResponse(task),
	})
}

// BacklogItemPathHandler handles requests under /api/orchestration/backlog/,
// dispatching by suffix: {id}, {id}/promote, reorder, and sync.
func (bh *BacklogHandler) BacklogItemPathHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/orchestration/backlog/")
	path = strings.Trim(path, "/")

	switch {
	case path == "reorder":
		bh.handleReorder(w, r)
		return
	case path == "sync":
		bh.handleSyncNow(w, r)
		return
	case strings.HasSuffix(path, "/promote"):
		bh.handlePromote(w, r, strings.TrimSuffix(path, "/promote"))
		return
	}

	taskID := path
	if taskID == "" {
		orihttp.BadRequest(w, "item id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		bh.handleGet(w, r, taskID)
	case http.MethodPut, http.MethodPatch:
		bh.handleUpdate(w, r, taskID)
	case http.MethodDelete:
		bh.handleDelete(w, r, taskID)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (bh *BacklogHandler) handleGet(w http.ResponseWriter, r *http.Request, taskID string) {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	item, err := bh.service.Get(workspaceID, taskID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "item": item})
}

func (bh *BacklogHandler) handleUpdate(w http.ResponseWriter, r *http.Request, taskID string) {
	var req struct {
		WorkspaceID  string    `json:"workspace_id"`
		Description  *string   `json:"description"`
		Details      *string   `json:"details"`
		Tags         *[]string `json:"tags"`
		Priority     *int      `json:"priority"`
		ReferenceURL *string   `json:"reference_url"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(req.WorkspaceID)
	}
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	task, err := bh.service.Update(workspaceID, taskID, workspace.BacklogUpdateInput{
		Description:  req.Description,
		Details:      req.Details,
		Tags:         req.Tags,
		Priority:     req.Priority,
		ReferenceURL: req.ReferenceURL,
	})
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "item": bh.itemResponse(task)})
}

func (bh *BacklogHandler) handleDelete(w http.ResponseWriter, r *http.Request, taskID string) {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if err := bh.service.Delete(workspaceID, taskID); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true})
}

func (bh *BacklogHandler) handlePromote(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	task, err := bh.service.Promote(workspaceID, taskID)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "item": bh.itemResponse(task)})
}

func (bh *BacklogHandler) handleReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req struct {
		WorkspaceID string   `json:"workspace_id"`
		OrderedIDs  []string `json:"ordered_ids"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	items, err := bh.service.Reorder(req.WorkspaceID, req.OrderedIDs)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "items": items})
}

func (bh *BacklogHandler) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if err := bh.service.SyncNow(workspaceID); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Sync failed", err)
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "sync": bh.service.SyncStatus(workspaceID)})
}
