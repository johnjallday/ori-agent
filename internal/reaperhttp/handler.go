// Package reaperhttp serves workspace-scoped live REAPER state. The handler
// accepts only a workspace ID; the loopback endpoint is resolved from trusted
// server-side state.
package reaperhttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type WorkspaceStore interface {
	reapersetup.RuntimeWorkspaceSource
	Get(string) (*workspace.Workspace, error)
}

type StateReader interface {
	Connected(context.Context) reaper.State
}

type Handler struct {
	store    WorkspaceStore
	provider userprofile.UserProvider
	client   StateReader
}

func NewHandler(store WorkspaceStore, provider userprofile.UserProvider, client StateReader) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{store: store, provider: provider, client: client}
}

func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	folderWorkspace, err := h.store.GetFolderWorkspace(ws.ID)
	if err != nil || folderWorkspace == nil {
		h.respondUnavailable(w)
		return
	}
	if !runtimeSelectsLiveReaper(folderWorkspace) {
		_ = orihttp.RespondSuccess(w, reaper.State{Applies: false, CheckedAt: time.Now().UTC()})
		return
	}
	state := h.client.Connected(r.Context())
	state.Applies = true
	_ = orihttp.RespondSuccess(w, state)
}

func runtimeSelectsLiveReaper(ws *workspace.Workspace) bool {
	if ws == nil {
		return false
	}
	state := ws.GetRuntimeState()
	contract := ws.RuntimeRequirementsSnapshot()
	if state == nil || contract == nil || state.SelectedModeID != "ori_assisted" {
		return false
	}
	mode, ok := contract.Mode(state.SelectedModeID)
	if !ok {
		return false
	}
	requiresLiveControl := false
	for _, key := range mode.Requires {
		if workspace.NormalizeRuntimeIdentifier(key) == reapersetup.ReaperLiveControlCapability {
			requiresLiveControl = true
			break
		}
	}
	if !requiresLiveControl {
		return false
	}
	requirement, ok := contract.Requirement(reapersetup.ReaperLiveControlCapability)
	return ok && workspace.NormalizeRuntimeIdentifier(requirement.Adapter) == reapersetup.ReaperLiveControlCapability
}

func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (*workspace.Workspace, bool) {
	if h == nil || h.store == nil || h.client == nil {
		h.respondUnavailable(w)
		return nil, false
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id is required")
		return nil, false
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil || ws == nil || !h.ownedByCurrentUser(r.Context(), ws) {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return nil, false
	}
	return ws, true
}

func (h *Handler) ownedByCurrentUser(ctx context.Context, ws *workspace.Workspace) bool {
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		logger.Warn("Failed to resolve current user for live REAPER state", logger.Fields{"category": "user_lookup_failed"})
		return false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.EqualFold(owner, userID)
}

func (h *Handler) respondUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
		orihttp.NewAPIError("reaper_unavailable", "Live REAPER state is not available for this workspace."))
}
