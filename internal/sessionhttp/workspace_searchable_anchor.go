package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/session"
)

// SetFolderSearch enables the optional filename effect for ordinary, reviewed
// anchored creation. The local Files boundary is narrower than workspace API
// access and is rechecked on both review and confirmation.
func (h *Handler) SetFolderSearch(s *filesearch.Service, allowed func(*http.Request, string) bool) {
	h.folderSearch, h.folderSearchAllowed = s, allowed
}

func (h *Handler) resolveSearchableAnchor(r *http.Request, owner string, req createWorkspaceRequest) (string, error) {
	if h.folderSearch == nil || h.folderSearchAllowed == nil || !h.folderSearchAllowed(r, owner) {
		return "", filesearch.ErrUnavailable
	}
	if req.FolderSelection == "" || req.AnchorPath != "" || req.BuildSessionID != "" || req.Kind == "group" || req.ProjectConnection != nil || req.Location != "" {
		return "", errors.New("choose one folder with the folder controls before review")
	}
	if err := h.folderSearch.CanCreate(r.Context(), owner, req.FolderSelection); err != nil {
		return "", err
	}
	return h.folderSearch.ReviewedRoot(r.Context(), owner, req.FolderSelection)
}

// Only a reviewed, still-linked folder supplies manager defaults. A missing
// suggestion leaves ordinary workspace staffing available; no model is called.
func (h *Handler) handleFolderManagerDraft(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.workspaceStore != nil && h.folderSearch != nil && h.folderSearchAllowed != nil {
		ws, err := h.workspaceStore.Get(id)
		if err == nil && ws != nil && ws.ID == id && h.folderSearchAllowed(r, ws.OwnerUserID) {
			if ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID()); err == nil && ref != nil && ref.WorkspaceID == id {
				if kind, ok := h.folderSearch.StaffingFolderKind(r.Context(), ws.OwnerUserID, ref.Path, filesearch.WorkspaceMatch{ID: id, Link: ref.ID}); ok {
					draft := map[string]string{"workspace_id": id, "name": filepath.Base(ref.Path) + " Manager", "brief": folderagent.WorkspaceManagerBrief(kind)}
					// Only offer the known local lead on a strict Blank workspace. An
					// ordinary attachment must not replace a holder or staff a Home.
					if provenance := h.workspaceTemplateProvenance(ws); provenance != nil && provenance.TemplateID == blankWorkspaceTemplateID && !h.workspaceIsAssistantProgram(ws) && blankLeadVacant(ws) {
						draft["lead_role_id"] = "ask-ori"
					}
					if h.folderManager != nil {
						if rec, e := h.folderManager.Record(ws.OwnerUserID, id); e == nil {
							draft["look_state"] = string(rec.State)
						} else if errors.Is(e, folderagent.ErrNoRecord) {
							draft["look_link"] = ref.ID
						}
					}
					orihttp.WriteJSON(w, draft)
					return
				}
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SetFolderManagerService(service *folderagent.Service) { h.folderManager = service }

// AssignedLookRoot checks host-owned identity again before the worker walks.
func (h *Handler) AssignedLookRoot(ctx context.Context, owner, id, agent, link string) (string, string, bool) {
	if h.workspaceStore == nil || h.folderSearch == nil || agent == "" || link == "" {
		return "", "", false
	}
	ws, e := h.workspaceStore.Get(id)
	if e != nil || ws == nil || ws.ID != id || ws.OwnerUserID != owner || ws.AnchorDirectoryReferenceID() != link {
		return "", "", false
	}
	instance, ok := ws.FindAgentInstance(agent, "")
	if !ok || instance.Name != agent {
		return "", "", false
	}
	if assignment, ok := ws.GetToolboxAssignment(instance.ID); ok && assignment.ToolboxID != "folder-agent-"+instance.ID {
		return "", "", false
	}
	ref, e := ws.GetDirectoryReference(link)
	if e != nil || ref == nil {
		return "", "", false
	}
	if _, ok := h.folderSearch.StaffingFolderKind(ctx, owner, ref.Path, filesearch.WorkspaceMatch{ID: id, Link: link}); !ok {
		return "", "", false
	}
	return ref.Path, ws.Name, true
}

func (h *Handler) handleFolderManagerLook(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	if h.folderManager == nil || h.workspaceStore == nil || h.folderSearchAllowed == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Folder review is unavailable.")
		return
	}
	ws, e := h.workspaceStore.Get(id)
	if e != nil || ws == nil || !h.folderSearchAllowed(r, ws.OwnerUserID) {
		_ = orihttp.RespondForbidden(w, "This folder is not available here.")
		return
	}
	var choice struct {
		Agent string `json:"agent"`
		Link  string `json:"link"`
		Brief string `json:"brief"`
	}
	if !orihttp.ParseJSONBody(w, r, &choice) {
		return
	}
	if err := h.folderManager.StartAssigned(r.Context(), ws.OwnerUserID, id, choice.Agent, choice.Link, choice.Brief); err != nil {
		_ = orihttp.RespondError(w, http.StatusConflict, "Could not start the first look. Reopen the workspace to check the folder, agent and saved status.")
		return
	}
	orihttp.WriteJSON(w, map[string]string{"workspace_id": id})
}

// The ordinary anchor checker may report a foreign workspace name or the
// identity read from a portable file. Neither is an authorized Files match.
func searchableAnchorRefusal(cause *anchorPathError) *anchorPathError {
	return anchorError("folder_unavailable", "This folder cannot be used for a new workspace. No search access was granted.", cause.status)
}

// Workspace creation is already durable. Verify the actual anchor, not the
// predicted name or an arbitrary primary link, before starting the search
// effect. A failed grant is not a reason to repeat or roll back creation.
func (h *Handler) finishSearchableAnchor(ctx context.Context, ws *session.Workspace, req createWorkspaceRequest, response map[string]any) {
	partial := "Workspace created. Its folder connection could not be verified."
	if req.AllowFilenameSearch {
		partial = "Workspace created, but its search connection was not saved. Choose the same folder again in Files to retry the connection without creating another workspace."
	}
	stored, err := h.workspaceStore.Get(ws.ID)
	if err != nil || stored == nil || stored.OwnerUserID != ws.OwnerUserID {
		response["search_warning"] = partial
		return
	}
	ref, err := stored.GetDirectoryReference(stored.AnchorDirectoryReferenceID())
	if err != nil || ref == nil || ref.WorkspaceID != ws.ID || ref.Path != req.AnchorPath {
		response["search_warning"] = partial
		return
	}
	response["directory_reference_id"] = ref.ID
	source, err := h.folderSearch.BindCreated(ctx, ws.OwnerUserID, req.FolderSelection, ws.ID, ref.ID, req.AllowFilenameSearch)
	if err != nil {
		response["search_warning"] = partial
		return
	}
	if req.AllowFilenameSearch {
		response["filename_source_id"] = source.ID
	}
}
