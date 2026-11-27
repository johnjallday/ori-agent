package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid map folders request")
		return
	}
	state := home.GetAssistantProgramState()
	if home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing || state.Declaration == nil {
		_ = orihttp.RespondNotFound(w, "Home unavailable")
		return
	}
	provider, reviewed := reviewedintegration.HomeProviderFor(scope.ProviderID)
	canAdd := reviewed && provider.ProgramID == scope.ProgramID && state.HomeProvider != nil && h.assistantLibraryProviderEvidence(scope, home)
	view, err := h.assistantLibraryStore().MapFolders(scope, home)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	view.CanAdd = canAdd
	_ = orihttp.RespondSuccess(w, view)
}
