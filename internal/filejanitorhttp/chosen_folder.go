package filejanitorhttp

import (
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Both phases require the trusted, workspace-scoped selection. Unlike setup,
// neither phase attaches that directory to any workspace or agent.
func (h *Handler) ChosenFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Selection filejanitor.ChosenSelection `json:"selection"`
		Token     string                      `json:"selection_token"`
		Digest    string                      `json:"plan_digest"`
		Key       string                      `json:"client_key"`
		Example   string                      `json:"example"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondForbidden(w, "Access unavailable.")
		return
	}
	var p string
	if req.Example != "" {
		if req.Token != "" || req.Selection.CandidateID == "" {
			_ = orihttp.RespondBadRequest(w, "Choose one destination.")
			return
		}
		req.Selection.Example = req.Example
		p, err = h.service.ResolveExamplePath(owner, id, req.Selection.CandidateID, req.Example)
	} else {
		resolver, ok := h.pathSelections.(interface {
			ResolveFor(string, string) (string, error)
		})
		if !ok {
			_ = orihttp.RespondServiceUnavailable(w, "Folder selection is unavailable.")
			return
		}
		p, err = resolver.ResolveFor(req.Token, id)
	}
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Choose the destination folder again.")
		return
	}
	checked, err := folderpath.Check(p, filejanitor.DefaultRootGuards())
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "This destination is unavailable or protected. Choose another folder.")
		return
	}
	b := r.PathValue("batchID")
	if r.URL.Query().Get("confirm") != "true" {
		preview, err := h.service.PreviewChosenFolder(r.Context(), owner, id, b, checked.Path, req.Selection)
		if err != nil {
			h.respondPictureError(w, err)
			return
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"preview": preview})
		return
	}
	result, err := h.service.FileChosenFolder(r.Context(), owner, id, b, checked.Path, req.Selection, req.Digest, req.Key)
	if err != nil {
		if errors.Is(err, filejanitor.ErrReviewPlanChanged) {
			_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("changed", "The files or destination changed. Nothing was filed; review the folder again."))
			return
		}
		h.respondReviewError(w, err, "Unable to file this selection.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"result": result})
}
