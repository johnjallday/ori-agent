package sessionhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// songCardResponse is the song card's whole payload. Linked is false only for
// a workspace that is not a Home's song; every other field is then empty.
type songCardResponse struct {
	Linked bool `json:"linked"`
	*projectlibrary.SongCard
	// Words are the Home's package words for the card (neutral without one).
	Words            *projectlibrary.Words `json:"words,omitempty"`
	HomeName         string                `json:"home_name,omitempty"`
	StatusLine       string                `json:"status_line,omitempty"`
	HomeRoute        string                `json:"home_route,omitempty"`
	HomeEntryRoute   string                `json:"home_entry_route,omitempty"`
	FactsSwitchRoute string                `json:"facts_switch_route,omitempty"`
}

// GetSongCard is the read-only card for one song workspace: its Home, library
// facts, last save, keeper, status line and open work, worded by the Home's
// pinned package. It reads stored Home and child records and the installed
// package's words only; it never scans, opens a project file, checks provider
// availability or calls a model. The child's own URL grants no wider read of
// the Home's catalog than this one projection.
func (h *Handler) GetSongCard(w http.ResponseWriter, r *http.Request) {
	child, owner, ok := h.ownedWorkspace(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid song card request")
		return
	}
	link := child.GetAssistantProjectLink()
	if link == nil || workspace.AssistantProjectLinkIsResource(link) || link.StationWorkspaceID == "" {
		_ = orihttp.RespondSuccess(w, songCardResponse{Linked: false})
		return
	}
	now := time.Now()
	// A song whose Home is gone, foreign or no longer a program Home keeps
	// its card with the child's own name, keeper and open work.
	fallback := projectlibrary.SongCard{Title: child.Name, KeeperAgentName: child.EntryAgentName(),
		OpenWork: child.TicketCounts()}
	home, err := h.workspaceTaskStore.Get(link.StationWorkspaceID)
	state := (*workspace.AssistantProgramState)(nil)
	if err == nil && home != nil && home.OwnerUserID == owner {
		state = home.GetAssistantProgramState()
	}
	if state == nil || state.Key.Normalize().OwnerUserID != owner {
		neutral := projectlibrary.NeutralWords()
		_ = orihttp.RespondSuccess(w, songCardResponse{Linked: true, SongCard: &fallback, Words: &neutral,
			StatusLine: projectlibrary.StatusLine(fallback, neutral, now)})
		return
	}
	key := state.Key.Normalize()
	scope := projectlibrary.Scope{OwnerUserID: owner, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	words := h.projectCardWords(home)
	card, err := projectlibrary.NewStore(h.workspaceTaskStore).SongCard(scope, child.ID, words)
	if errors.Is(err, projectlibrary.ErrConflict) {
		card, err = fallback, nil
		card.HomeID = home.ID
	}
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	response := songCardResponse{Linked: true, SongCard: &card, Words: &words, HomeName: home.Name,
		StatusLine: projectlibrary.StatusLine(card, words, now)}
	if workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		base := "/workspaces/" + url.PathEscape(home.FolderSlug)
		response.HomeRoute = base
		if card.EntryID != "" {
			response.HomeEntryRoute = base + "/assistant#projectLibraryPanel"
		}
		if card.FactsState == projectlibrary.FactsStateOff {
			response.FactsSwitchRoute = base + "/assistant#projectLibrarySongDetails"
		}
	}
	_ = orihttp.RespondSuccess(w, response)
}

// ownedWorkspace loads the path's workspace for its owner; anything else is
// the same 404 so a foreign ID reveals nothing.
func (h *Handler) ownedWorkspace(w http.ResponseWriter, r *http.Request) (*workspace.Workspace, string, bool) {
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return nil, "", false
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return nil, "", false
	}
	id := strings.TrimSpace(r.PathValue("workspaceID"))
	found, err := h.workspaceTaskStore.Get(id)
	if err != nil || found == nil || found.ID != id || found.OwnerUserID != owner {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return nil, "", false
	}
	return found, owner, true
}
