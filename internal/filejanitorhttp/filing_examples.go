package filejanitorhttp

import (
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"net/http"
)

func (h *Handler) EditExample(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Choice string `json:"choice"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.Choice != "remember" && req.Choice != "forget" {
		_ = orihttp.RespondBadRequest(w, "Choose whether to remember or forget this example.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondForbidden(w, "Workspace unavailable.")
		return
	}
	if err = h.service.EditFilingExample(owner, id, r.PathValue("actionID"), req.Choice == "forget"); err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("example_unavailable", "This example changed or is unavailable. Review it again."))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]bool{"success": true})
}
