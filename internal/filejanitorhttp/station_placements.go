package filejanitorhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

func (h *Handler) StationPlacements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondForbidden(w, "Your workspace identity is unavailable.")
		return
	}
	l, err := h.service.ReadPlacements(owner, id)
	if err != nil {
		h.respondPictureError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"placements": l})
}
func (h *Handler) SavePlacements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision string   `json:"revision"`
		Rows     []string `json:"rows"`
	}
	owner, ok := h.stationReadRequest(w, r, &req)
	if !ok {
		return
	}
	if err := h.service.SaveReceiptPlacements(r.Context(), owner, id, r.PathValue("filingID"), req.Revision, req.Rows); err != nil {
		_ = orihttp.RespondConflict(w, "Placements changed. Check Settings before trying again.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) RemovePlacement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision string `json:"revision"`
		Row      string `json:"row"`
	}
	owner, ok := h.stationReadRequest(w, r, &req)
	if !ok {
		return
	}
	if err := h.service.RemovePlacement(owner, id, req.Revision, req.Row); err != nil {
		_ = orihttp.RespondConflict(w, "Placements changed. Check Settings before trying again.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
