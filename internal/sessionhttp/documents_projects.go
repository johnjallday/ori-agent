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
	Action string `json:"action"`
	Parent string `json:"parent,omitempty"`
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
	var view folderagent.ProjectLook
	var err error
	switch q.Action {
	case "folders":
		view, err = d.Folders()
	case "find":
		view, err = d.Find(r.Context(), q.Parent, nil)
	default:
		_ = orihttp.RespondBadRequest(w, "Choose a project discovery action.")
		return
	}
	if current, ok := h.projectDiscovery(r, id); err != nil || !ok || current != d {
		_ = orihttp.RespondError(w, http.StatusConflict, "This look is unavailable or the folder changed. Choose it again.")
		return
	}
	orihttp.WriteJSON(w, view)
}
