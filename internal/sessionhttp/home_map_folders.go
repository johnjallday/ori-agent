package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// GetHomeMapFolders is an owner-only read. A feed can start before the Home's
// library is initialized, but not when its pinned provider is read-only.
func (h *Handler) GetHomeMapFolders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	files := r.URL.RawQuery == "files=1"
	if r.URL.RawQuery != "" && !files {
		_ = orihttp.RespondBadRequest(w, "Invalid map folders request")
		return
	}
	state := home.GetAssistantProgramState()
	if home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing || state == nil || state.Declaration == nil {
		_ = orihttp.RespondNotFound(w, "Home unavailable")
		return
	}
	if files {
		view, err := h.assistantLibraryStore().ReadFilesMarks(scope, home)
		if err != nil {
			respondLibraryReadError(w, err)
			return
		}
		_ = orihttp.RespondSuccess(w, view)
		return
	}
	canAdd := h.MapFeedProvider(scope.OwnerUserID, home) != ""
	view, err := h.assistantLibraryStore().MapFolders(scope, home)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	view.CanAdd = canAdd
	_ = orihttp.RespondSuccess(w, view)
}

// MapFeedProvider is the shared eligibility check for showing Add folder and
// creating a workspace from its hire card. No library initialization is needed.
func (h *Handler) MapFeedProvider(owner string, home *workspace.Workspace) string {
	if home == nil || owner == "" || home.OwnerUserID != owner || home.Kind != "group" || home.GetStatus() == workspace.StatusMissing || home.GetStatus() == workspace.StatusTrashed {
		return ""
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil || state.HomeProvider == nil {
		return ""
	}
	key := state.Key.Normalize()
	scope := projectlibrary.Scope{OwnerUserID: owner, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	provider, reviewed := reviewedintegration.HomeProviderFor(key.PluginID)
	if key.OwnerUserID != owner || !reviewed || provider.ProgramID != key.ProgramID || !h.assistantLibraryStore().CanFeedHome(scope, home) {
		return ""
	}
	return provider.Key
}
