package sessionhttp

import (
	"net/http"
	"sort"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxLibraryHomes bounds the handoff list; a person has a few Homes, not many.
const maxLibraryHomes = 32

// AssistantLibraryHome names one of the owner's program Homes whose saved
// catalog its own Manager can organize. It is a navigation target only.
type AssistantLibraryHome struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Route string `json:"route"`
}

// ListAssistantLibraryHomes lists the owner's program Homes with an initialized
// library, for the PA's "organize inside a Home" handoff. It returns names and
// routes only: no catalog content, roots, Manager, provider or planning
// authority, and it initializes, upgrades or reads no library.
// GET /api/assistant-program/library-homes
func (h *Handler) ListAssistantLibraryHomes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Library Homes take no arguments")
		return
	}
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "Library Homes are unavailable")
		return
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		_ = orihttp.RespondNotFound(w, "Library Homes are unavailable")
		return
	}
	listed, err := h.workspaceTaskStore.ListActive()
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Library Homes are unavailable")
		return
	}
	homes := []AssistantLibraryHome{}
	for _, item := range listed {
		if item == nil || item.OwnerUserID != owner {
			continue
		}
		// A listing may be a metadata projection; decide on the full record.
		ws, err := h.workspaceTaskStore.Get(item.ID)
		if err != nil || ws == nil || ws.ID != item.ID || ws.OwnerUserID != owner || ws.GetStatus() != workspace.StatusActive || strings.TrimSpace(ws.FolderSlug) == "" {
			continue
		}
		state := ws.GetAssistantProgramState()
		if state == nil || state.Key.Normalize().OwnerUserID != owner || len(state.ProjectLibrary) == 0 {
			continue
		}
		homes = append(homes, AssistantLibraryHome{ID: ws.ID, Name: ws.Name, Route: "/workspaces/" + ws.FolderSlug})
	}
	sort.Slice(homes, func(i, j int) bool {
		if homes[i].Name != homes[j].Name {
			return homes[i].Name < homes[j].Name
		}
		return homes[i].ID < homes[j].ID
	})
	if len(homes) > maxLibraryHomes {
		homes = homes[:maxLibraryHomes]
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"homes": homes})
}
