// Package reaperhttp serves workspace-scoped live REAPER state and actions. The handler
// accepts only a workspace ID; the project path and loopback endpoint are
// resolved from trusted server-side state.
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
	ReadState(context.Context, reaper.ProjectSource) (reaper.State, error)
}

type ActionCatalog interface {
	List() ([]reaper.Action, error)
	Find(string) (reaper.Action, bool, error)
}

type ScriptLibrary interface {
	List() ([]reaper.Script, error)
	Read(string) (reaper.Script, error)
	Create(reaper.ScriptInput) (reaper.Script, error)
	Update(string, reaper.ScriptInput) (reaper.Script, error)
	Delete(string) error
}

type ScriptRunner interface {
	RunScript(context.Context, string) (reaper.ScriptRunResult, error)
}

type Handler struct {
	store         WorkspaceStore
	provider      userprofile.UserProvider
	client        StateReader
	catalog       ActionCatalog
	scriptLibrary ScriptLibrary
	scriptRunner  ScriptRunner
	trackRunner   TrackEditRunner
	proposals     *proposalStore
	undos         *undoStore
}

func NewHandler(store WorkspaceStore, provider userprofile.UserProvider, client StateReader, catalog ActionCatalog) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{
		store: store, provider: provider, client: client, catalog: catalog,
		proposals: newProposalStore(), undos: newUndoStore(),
	}
}

func (h *Handler) SetScriptServices(library ScriptLibrary, runner ScriptRunner) {
	if h != nil {
		h.scriptLibrary = library
		h.scriptRunner = runner
	}
}

// SetTrackEditRunner supplies the guarded single-track edit path. Track
// editing stays unavailable until it is set, which is also how a workspace
// with no installed runner degrades.
func (h *Handler) SetTrackEditRunner(runner TrackEditRunner) {
	if h != nil {
		h.trackRunner = runner
	}
}

// trackEditingAvailable answers the one question the console needs to decide
// between interactive strips and a read-only list.
func (h *Handler) trackEditingAvailable(ctx context.Context) bool {
	return h != nil && h.trackRunner != nil && h.trackRunner.Available(ctx)
}

func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	project, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return
	}
	if !applies {
		_ = orihttp.RespondSuccess(w, reaper.State{
			Applies:   false,
			PlayState: "unknown",
			Tracks:    []reaper.Track{},
			CheckedAt: time.Now().UTC(),
		})
		return
	}
	state, err := h.client.ReadState(r.Context(), project)
	if err != nil {
		logger.Warn("Live REAPER state request failed", logger.Fields{"category": "reaper_state_failed"})
		_ = orihttp.RespondAPIError(w, http.StatusBadGateway,
			orihttp.NewAPIError("reaper_state_failed", "Live REAPER state could not be read."))
		return
	}
	state.Applies = true
	// Probe the runner only while REAPER is reachable: a disconnected session
	// already renders the offline panel, and strips never appear there.
	if state.Connected {
		state.TrackEditingAvailable = h.trackEditingAvailable(r.Context())
	}
	_ = orihttp.RespondSuccess(w, state)
}

func (h *Handler) projectSource(workspaceID string) (reaper.ProjectSource, bool, error) {
	folderWorkspace, err := h.store.GetFolderWorkspace(workspaceID)
	if err != nil {
		return reaper.ProjectSource{}, false, err
	}
	if folderWorkspace == nil {
		return reaper.ProjectSource{}, false, reaper.ErrClientUnavailable
	}
	if !runtimeSelectsLiveReaper(folderWorkspace) {
		return reaper.ProjectSource{}, false, nil
	}
	projectPath, err := reapersetup.AuthoritativeProject(h.store, workspaceID)
	if err != nil {
		return reaper.ProjectSource{}, true, err
	}
	entryPath, err := workspace.GetProjectEntryPath(folderWorkspace.SharedData)
	if err != nil {
		return reaper.ProjectSource{}, true, err
	}
	return reaper.ProjectSource{Path: projectPath, EntryPath: entryPath}, true, nil
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
