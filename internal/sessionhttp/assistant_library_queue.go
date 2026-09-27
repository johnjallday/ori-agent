package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Queue endpoints are owner-only navigation receipts. None of them invokes
// project creation, scanning, root authorization or a child agent.
func (h *Handler) GetAssistantLibraryQueue(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	view, err := h.assistantLibraryStore().CurrentActivationQueue(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

func (h *Handler) StartAssistantLibraryQueue(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var input struct {
		IDs        []string `json:"ids"`
		RequestKey string   `json:"request_key"`
	}
	if !decodeLibraryAction(w, r, &input) {
		return
	}
	queue, replay, err := h.assistantLibraryStore().StartActivationQueue(scope, input.IDs, input.RequestKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"queue": queue, "replay": replay})
}

func (h *Handler) ProgressAssistantLibraryQueue(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var input struct {
		EntryID    string `json:"entry_id"`
		Action     string `json:"action"`
		IfRevision int64  `json:"if_revision"`
		RequestKey string `json:"request_key"`
	}
	if !decodeLibraryAction(w, r, &input) {
		return
	}
	queue, replay, err := h.assistantLibraryStore().ProgressActivationQueue(scope, r.PathValue("queueID"),
		input.EntryID, input.Action, input.RequestKey, input.IfRevision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"queue": queue, "replay": replay})
}

func (h *Handler) DiscardAssistantLibraryQueue(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var input struct {
		IfRevision int64  `json:"if_revision"`
		RequestKey string `json:"request_key"`
		Confirm    bool   `json:"confirm"`
	}
	if !decodeLibraryAction(w, r, &input) {
		return
	}
	if !input.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm queue discard")
		return
	}
	replay, err := h.assistantLibraryStore().DiscardActivationQueue(scope, r.PathValue("queueID"), input.RequestKey, input.IfRevision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"discarded": true, "replay": replay})
}
