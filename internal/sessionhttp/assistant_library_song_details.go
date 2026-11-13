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
	// ReaderName names the app plugin that reads them, as the reviewed list gives
	// it: the approval says the plugin reads the files and Ori keeps only the
	// listed facts (assistant-reads-projects FR44, FR45). Empty when no
	// installed plugin reads this Home's songs.
	ReaderName string `json:"reader_name,omitempty"`
	// ReaderNotice is true once, for a Home that approved song details before the
	// app plugin read them: the page tells it so and links the switch. It is not
	// asked again; acknowledging it is recorded on the consent (FR46).
	ReaderNotice bool `json:"reader_notice,omitempty"`
	// ReaderUpdate names a reviewed app plugin that is installed but declares no
	// reader, so no installed plugin reads this Home's songs: the page says
	// "Update <plugin> to read songs" where the details would be, never a silent
	// blank (assistant-reads-projects FR49). Empty when a plugin reads, or none
	// is installed.
	ReaderUpdate string `json:"reader_update,omitempty"`
}

// librarySongDetailsFor is librarySongDetails with the app plugin that reads the
// songs named, from the installed plugins, and the one-time notice for a Home
// that approved song details before it did.
func (h *Handler) librarySongDetailsFor(home *workspace.Workspace) librarySongDetailsView {
	view := librarySongDetails(home)
	if h != nil && h.installedPluginLister != nil {
		if installed, err := h.installedPluginLister.List(); err == nil {
			view.ReaderName = projectlibrary.ReaderDisplayName(installed)
			if view.ReaderName == "" {
				view.ReaderUpdate = projectlibrary.ReaderUpdateName(installed)
			}
		}
	}
	if view.ReaderName != "" && view.State == workspace.SongDetailsOn && home != nil {
		view.ReaderNotice = home.GetAssistantProgramState().GetSongDetailsConsent().NeedsReaderNotice()
	}
	return view
}

// AcknowledgeSongDetailsReaderNotice handles
// POST /api/workspaces/{workspaceID}/assistant-program/library/song-details/notice
// {request_id}: the owner saw that the app plugin now reads the project files.
// It records that on the Home's consent and changes nothing else: not what is
// read, not the switch. Asking twice is a no-op.
func (h *Handler) AcknowledgeSongDetailsReaderNotice(w http.ResponseWriter, r *http.Request) {
	_, home, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
	}
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 120 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	if err := workspace.NewSongDetailsConsents(h.workspaceTaskStore).AcknowledgeReaderNotice(home.ID); err != nil {
		h.respondSongDetailsError(w, home, err)
		return
	}
	current, _ := h.workspaceTaskStore.Get(home.ID)
	_ = orihttp.RespondSuccess(w, map[string]any{"song_details": h.librarySongDetailsFor(current)})
}

// librarySongDetails reads the switch from the Home the request resolved.
func librarySongDetails(home *workspace.Workspace) librarySongDetailsView {
	view := librarySongDetailsView{State: workspace.SongDetailsNone, AppName: folderdigest.ReviewedAppName()}
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
		h.respondSongDetailsError(w, home, projectlibrary.ErrSongDetailsNotGranted)
		return
	}
	state, err := h.assistantLibraryStore().SetSongDetails(scope, request.Enabled, request.Names)
	if err != nil {
		current, _ := h.workspaceTaskStore.Get(home.ID)
		h.respondSongDetailsError(w, current, err)
		return
	}
	if !request.Enabled && h.songDetailsOff != nil {
		// Also on a repeated off: it only ever removes, so it can be told twice.
		h.songDetailsOff(home.ID)
	}
	view := h.librarySongDetailsFor(nil)
	if current, _ := h.workspaceTaskStore.Get(home.ID); current != nil {
		view = h.librarySongDetailsFor(current)
	} else {
		view.State = state
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"song_details": view})
}

func (h *Handler) respondSongDetailsError(w http.ResponseWriter, home *workspace.Workspace, err error) {
	body := map[string]any{"song_details": h.librarySongDetailsFor(home)}
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
