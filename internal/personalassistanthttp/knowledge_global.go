package personalassistanthttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// GlobalMemoryWorkspaces is what the global-memory routes read about
// workspaces: a source workspace's current name and page slug, and the
// active workspaces a copy can live in.
type GlobalMemoryWorkspaces interface {
	Get(id string) (*workspace.Workspace, error)
	ListActive() ([]*workspace.Workspace, error)
}

// SetGlobalMemoryWorkspaces wires the workspace reader. Without it, review
// items keep the workspace name recorded when an agent proposed the fact.
func (h *Handler) SetGlobalMemoryWorkspaces(workspaces GlobalMemoryWorkspaces) {
	if h != nil {
		h.workspaces = workspaces
	}
}

// attributeReviewItems refreshes each workspace proposal's workspace name and
// adds the page slug the Profile chip links to. A workspace that is gone,
// trashed or missing keeps its recorded name and gets no link.
func (h *Handler) attributeReviewItems(items []personalassistant.KnowledgeReviewItem) {
	if h == nil || h.workspaces == nil {
		return
	}
	for i := range items {
		source := &items[i].Source
		if source.WorkspaceID == "" {
			continue
		}
		ws, err := h.workspaces.Get(source.WorkspaceID)
		if err != nil || ws == nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
			continue
		}
		if name := strings.TrimSpace(ws.Name); name != "" {
			source.WorkspaceName = name
		}
		source.WorkspaceSlug = ws.FolderSlug
	}
}

// liveWorkspace returns a workspace that exists and is neither trashed nor
// missing, or nil.
func (h *Handler) liveWorkspace(workspaceID string) *workspace.Workspace {
	if h == nil || h.workspaces == nil || strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	ws, err := h.workspaces.Get(workspaceID)
	if err != nil || ws == nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return nil
	}
	return ws
}

// workspaceMemoryHref opens the workspace's Memory station, where a copied
// fact lands; "" for a workspace without a usable address.
func workspaceMemoryHref(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	return workspace.StationURL(ws.FolderSlug, "memory")
}

type globalFactCopyRequest struct {
	WorkspaceID string `json:"workspace_id"`
	IfVersion   int64  `json:"if_version"`
}

type globalFactCopy struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Href        string `json:"href,omitempty"`
}

// CopyGlobalFact handles POST /api/personal-assistant/knowledge/{item_id}/copy.
// The browser names only the approved fact, the version it is looking at and
// a target workspace; the server reads the canonical text and writes one
// ordinary "global:<item-id>" entry there.
func (h *Handler) CopyGlobalFact(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	itemID, ok := h.globalFactID(w, r)
	if !ok {
		return
	}
	var body globalFactCopyRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil || body.IfVersion < 1 {
		orihttp.BadRequest(w, "Choose a workspace for the current version of this fact")
		return
	}
	target := h.liveWorkspace(body.WorkspaceID)
	if target == nil {
		orihttp.NotFound(w, "That workspace is not available")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	created, err := h.knowledgeReview.CopyGlobalFact(r.Context(), userID, itemID, body.IfVersion, target.ID, time.Now().Format("2006-01-02"))
	if err != nil {
		writeGlobalFactError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"created": created, "workspace_id": target.ID, "href": workspaceMemoryHref(target)})
}

// GetGlobalFactCopies handles GET /api/personal-assistant/knowledge/{item_id}/copies:
// the active workspaces whose MEMORY.md holds a copy of this fact. It scans
// on demand and reads only; it also works after the fact was forgotten.
func (h *Handler) GetGlobalFactCopies(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	itemID, ok := h.globalFactID(w, r)
	if !ok {
		return
	}
	if h.workspaces == nil {
		orihttp.ServiceUnavailable(w, "Workspace storage is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	active, err := h.workspaces.ListActive()
	if err != nil {
		orihttp.ServiceUnavailable(w, "Workspaces could not be listed. Try again.")
		return
	}
	byID := make(map[string]*workspace.Workspace, len(active))
	ids := make([]string, 0, len(active))
	for _, ws := range active {
		if ws == nil || ws.ID == "" {
			continue
		}
		byID[ws.ID] = ws
		ids = append(ids, ws.ID)
	}
	holders, err := h.knowledgeReview.GlobalFactCopies(r.Context(), userID, itemID, ids)
	if err != nil {
		writeGlobalFactError(w, err)
		return
	}
	copies := make([]globalFactCopy, 0, len(holders))
	for _, id := range holders {
		ws := byID[id]
		copies = append(copies, globalFactCopy{WorkspaceID: id, Name: ws.Name, Slug: ws.FolderSlug, Href: workspaceMemoryHref(ws)})
	}
	orihttp.Success(w, map[string]any{"copies": copies})
}

// RemoveGlobalFactCopy handles DELETE
// /api/personal-assistant/knowledge/{item_id}/copies/{workspace_id}: it
// deletes that workspace's copied line and nothing else.
func (h *Handler) RemoveGlobalFactCopy(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodDelete) {
		return
	}
	itemID, ok := h.globalFactID(w, r)
	if !ok {
		return
	}
	target := h.liveWorkspace(r.PathValue("workspace_id"))
	if target == nil {
		orihttp.NotFound(w, "That workspace is not available")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	if err := h.knowledgeReview.RemoveGlobalFactCopy(r.Context(), userID, itemID, target.ID); err != nil {
		writeGlobalFactError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"removed": true, "workspace_id": target.ID})
}

func (h *Handler) globalFactID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.knowledgeReview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, personalassistant.GlobalMemoryUnavailableMessage)
		return "", false
	}
	itemID := r.PathValue("item_id")
	if _, err := uuid.Parse(itemID); err != nil {
		orihttp.BadRequest(w, "Check the fact and try again")
		return "", false
	}
	return itemID, true
}

func writeGlobalFactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrGlobalMemoryUnavailable):
		orihttp.Conflict(w, personalassistant.GlobalMemoryUnavailableMessage)
	case errors.Is(err, personalassistant.ErrValidation):
		orihttp.BadRequest(w, "Choose a workspace other than your Personal HQ")
	case errors.Is(err, personalassistant.ErrNotFound), errors.Is(err, workspace.ErrMemoryEntryNotFound):
		orihttp.NotFound(w, "No copy of this fact was found")
	case errors.Is(err, personalassistant.ErrConflict):
		orihttp.Conflict(w, "This fact changed or is not approved. Refresh and try again")
	default:
		orihttp.ServiceUnavailable(w, "Global memory is temporarily unavailable")
	}
}
