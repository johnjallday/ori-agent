package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// These endpoints expose saved, inert owner history only. No Manager identity
// is constructed, no model runs, and no installed provider or source is probed.
func (h *Handler) GetAssistantLibraryArrangements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid arrangement history request")
		return
	}
	refs, err := h.assistantLibraryStore().ArrangementRefs(r.Context(), scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"arrangements": refs})
}

func (h *Handler) GetAssistantLibraryArrangement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid arrangement history request")
		return
	}
	history, err := h.assistantLibraryStore().ReadArrangementHistory(r.Context(), scope, r.PathValue("arrangementID"))
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, history)
}
