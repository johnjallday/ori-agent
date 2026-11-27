package sessionhttp

import (
	"net/http"
	"os"
	"time"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

type projectAction struct {
	Action    string `json:"action"`
	Parent    string `json:"parent,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

func (h *Handler) projectDiscovery(r *http.Request, id string) (folderagent.ProjectDiscovery, bool) {
	var no folderagent.ProjectDiscovery
	if h.workspaceStore == nil || h.guidedLookAllowed == nil {
		return no, false
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil || ws == nil || ws.ID != id || !h.guidedLookAllowed(r, ws.OwnerUserID) {
		return no, false
	}
	look, ok := h.guidedDocumentsLook(r.Context(), ws)
	if !ok || look.verification != "" || look.filingBinding != "" {
		return no, false
	}
	info, err := os.Lstat(look.root) // #nosec G304 G703 -- server-verified Documents creation receipt, metadata only
	if err != nil || !info.IsDir() || directoryid.Witness(info) == "" || h.documentsWitness(look.root, time.Time{}) != look.witness {
		return no, false
	}
	return folderagent.ProjectDiscovery{Root: look.root, Witness: directoryid.Witness(info), Binding: look.op + ":" + look.witness}, true
}

func (h *Handler) handleDocumentsProjects(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	d, ok := h.projectDiscovery(r, id)
	if !ok {
		_ = orihttp.RespondForbidden(w, "The Documents folder could not be verified.")
		return
	}
	var q projectAction
	if !decodeBuildBody(w, r, &q) {
		return
	}
	h.projectDiscoveryMu.Lock()
	defer h.projectDiscoveryMu.Unlock()
	var view folderagent.ProjectLook
	var err error
	switch q.Action {
	case "folders":
		view, err = d.Folders()
	case "find", "ignore", "open":
		var ignored map[string]bool
		ignored, err = h.ignoredProjects(r.Context(), id)
		if err == nil {
			view, err = d.Find(r.Context(), q.Parent, ignored)
		}
	default:
		_ = orihttp.RespondBadRequest(w, "Choose a project discovery action.")
		return
	}
	if current, ok := h.projectDiscovery(r, id); err != nil || !ok || current != d {
		_ = orihttp.RespondError(w, http.StatusConflict, "This look is unavailable or the folder changed. Choose it again.")
		return
	}
	if q.Action == "ignore" || q.Action == "open" {
		for _, candidate := range view.Suggestions {
			if candidate.Ref != q.Candidate {
				continue
			}
			if q.Action == "ignore" {
				_, err = h.store.DB().ExecContext(r.Context(), `INSERT OR IGNORE INTO ignored_documents_projects(manager_id,folder_ref) VALUES (?,?)`, id, candidate.Identity)
				if err == nil {
					orihttp.WriteJSON(w, map[string]bool{"ignored": true})
					return
				}
			} else {
				var route string
				route, err = h.openCourse(r, id, d, candidate)
				if err == nil {
					orihttp.WriteJSON(w, map[string]string{"route": route})
					return
				}
			}
			break
		}
		_ = orihttp.RespondError(w, http.StatusConflict, "Setup could not be verified. Check your workspaces and look again.")
		return
	}
	orihttp.WriteJSON(w, view)
}
