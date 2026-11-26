package filejanitorhttp

import (
	"encoding/json"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"io"
	"net/http"
)

func (h *Handler) StationMethod(w http.ResponseWriter, r *http.Request) {
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
	var view filejanitor.StationMethod
	if r.Method == http.MethodGet {
		view, err = h.service.StationMethod(r.Context(), owner, id)
	} else {
		var b struct {
			Revision string `json:"revision"`
			Method   string `json:"method"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		if d.Decode(&b) != nil || d.Decode(&struct{}{}) != io.EOF {
			_ = orihttp.RespondBadRequest(w, "Choose one filing method.")
			return
		}
		view, err = h.service.ChooseStationMethod(r.Context(), owner, id, b.Revision, b.Method)
	}
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("method_unavailable", "The folder or its choices changed. Check folder access and review the choices again."))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"layout": view})
}
