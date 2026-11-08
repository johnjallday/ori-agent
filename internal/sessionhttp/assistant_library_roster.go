package sessionhttp

import (
	"net/http"
	"net/url"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type rosterRowResponse struct {
	projectlibrary.RosterRow
	WorkspaceRoute string `json:"workspace_route,omitempty"`
}

type rosterResponse struct {
	Rows  []rosterRowResponse  `json:"rows"`
	Total int                  `json:"total"`
	Words projectlibrary.Words `json:"words"`
}

// GetAssistantLibraryRoster lists the Home's connected songs with each one's
// keeper, last accepted session and open work, for Home Details' "Songs and
// assistants" section and the map tiles' facts. Read-only and owner-scoped:
// the keeper's name is the only thing read from a child's roster, and no
// agent record, prompt or memory is copied into the Home.
func (h *Handler) GetAssistantLibraryRoster(w http.ResponseWriter, r *http.Request) {
	scope, station, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid roster request")
		return
	}
	rows, err := projectlibrary.NewStore(h.workspaceTaskStore).Roster(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	response := rosterResponse{Rows: make([]rosterRowResponse, 0, len(rows)), Total: len(rows),
		Words: h.projectCardWords(station)}
	for _, row := range rows {
		item := rosterRowResponse{RosterRow: row}
		if workspace.IsCanonicalWorkspaceSlug(row.FolderSlug) {
			item.WorkspaceRoute = "/workspaces/" + url.PathEscape(row.FolderSlug)
		}
		response.Rows = append(response.Rows, item)
	}
	_ = orihttp.RespondSuccess(w, response)
}
