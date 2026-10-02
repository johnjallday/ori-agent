package sessionhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Home's song-details switch (D6): whether each scan reads every project's
// tempo, length and track count. Only a Home that consented on its setup card
// has the switch; each change is the owner's, on this Home only, and none is
// an agent tool.

type librarySongDetailsView struct {
	// State is none (no consent: no switch), on or off.
	State string `json:"state"`
	// AppName names whose project files are read, from the host's marker
	// table, so the switch's note can say so.
	AppName string `json:"app_name,omitempty"`
}

// librarySongDetails reads the switch from the Home the request resolved.
func librarySongDetails(home *workspace.Workspace) librarySongDetailsView {
	view := librarySongDetailsView{State: workspace.SongDetailsNone, AppName: folderdigest.FactsAppName()}
	if home != nil {
		view.State = home.GetAssistantProgramState().GetSongDetailsConsent().State()
	}
	return view
}

// SetAssistantLibrarySongDetails handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/song-details
// {request_id, enabled}. Off clears every stored song fact in the same Home
// write; on (only on a Home that consented at setup) starts no scan. Setting
// the state the Home already has is a no-op, so a retried request_id is too.
func (h *Handler) SetAssistantLibrarySongDetails(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
		Enabled   bool   `json:"enabled"`
	}
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 120 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	if librarySongDetails(home).State == workspace.SongDetailsNone {
		respondSongDetailsError(w, home, projectlibrary.ErrSongDetailsNotGranted)
		return
	}
	state, err := h.assistantLibraryStore().SetSongDetails(scope, request.Enabled)
	if err != nil {
		current, _ := h.workspaceTaskStore.Get(home.ID)
		respondSongDetailsError(w, current, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"song_details": librarySongDetailsView{State: state, AppName: folderdigest.FactsAppName()}})
}

func respondSongDetailsError(w http.ResponseWriter, home *workspace.Workspace, err error) {
	body := map[string]any{"song_details": librarySongDetails(home)}
	switch {
	case errors.Is(err, projectlibrary.ErrSongDetailsNotGranted):
		body["error"], body["reason"] = "This Home was not set up to read song details.", "song_details_not_granted"
	case errors.Is(err, projectlibrary.ErrUnavailable):
		body["error"], body["reason"] = "This Home is read-only right now, so song details cannot be turned on.", "provider_unavailable"
	default:
		body["error"], body["reason"] = "The Home's song details could not be changed. Reload and try again.", "song_details_unchanged"
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, body)
}
