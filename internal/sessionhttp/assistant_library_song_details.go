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
// tempo, length and track count and, when the owner agreed to it, the track
// names, markers and regions typed into each project. Only a Home that consented
// (on a setup card, or to the folder agent's offer to learn the songs) has the
// switch; each change is the owner's, on this Home only, and none is an agent
// tool.

type librarySongDetailsView struct {
	// State is none (no consent: no switch), on or off.
	State string `json:"state"`
	// Names is true while the consent is on and also reaches track names,
	// markers and regions; a numbers-only consent (the older folder card's) is
	// false, and so is any switch that is off.
	Names bool `json:"names"`
	// AppName names whose project files are read, from the host's marker
	// table, so the switch's note can say so.
	AppName string `json:"app_name,omitempty"`
}

// librarySongDetails reads the switch from the Home the request resolved.
func librarySongDetails(home *workspace.Workspace) librarySongDetailsView {
	view := librarySongDetailsView{State: workspace.SongDetailsNone, AppName: folderdigest.FactsAppName()}
	if home != nil {
		consent := home.GetAssistantProgramState().GetSongDetailsConsent()
		view.State, view.Names = consent.State(), consent.CoversNames()
	}
	return view
}

// SetSongDetailsOffHook registers what is told, with the Home's id, after a Home's
// song-details switch has been turned off and its stored facts cleared. The
// server uses it to drop what the folder's agent wrote from those facts.
func (h *Handler) SetSongDetailsOffHook(hook func(homeID string)) {
	if h != nil {
		h.songDetailsOff = hook
	}
}

// SetAssistantLibrarySongDetails handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/song-details
// {request_id, enabled, names}. Off clears every stored song fact (numbers,
// track names, markers and regions) in the same Home write; on (only on a Home
// that consented before) starts no scan. names says which wording the owner
// agreed to when turning on: true is the full one, track names, markers and
// regions included; false resumes the numbers only. Setting the state the Home
// already has is a no-op, so a retried request_id is too.
func (h *Handler) SetAssistantLibrarySongDetails(w http.ResponseWriter, r *http.Request) {
	scope, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
		Enabled   bool   `json:"enabled"`
		Names     bool   `json:"names"`
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
	state, err := h.assistantLibraryStore().SetSongDetails(scope, request.Enabled, request.Names)
	if err != nil {
		current, _ := h.workspaceTaskStore.Get(home.ID)
		respondSongDetailsError(w, current, err)
		return
	}
	if !request.Enabled && h.songDetailsOff != nil {
		// Also on a repeated off: it only ever removes, so it can be told twice.
		h.songDetailsOff(home.ID)
	}
	view := librarySongDetails(nil)
	if current, _ := h.workspaceTaskStore.Get(home.ID); current != nil {
		view = librarySongDetails(current)
	} else {
		view.State = state
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"song_details": view})
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
