package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Forget is an owner-reviewed removal of one historical Home catalog record
// and its Home-authored studio sessions. It does not delete a project
// workspace, root grant or source file. Active sources and linked entries refuse.
func (h *Handler) ReviewAssistantLibraryForget(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request struct {
		Revision int64 `json:"revision"`
	}
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewForget(scope, r.PathValue("entryID"), request.Revision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryForget(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm removal of this saved Home record and its sessions")
		return
	}
	replay, err := h.assistantLibraryStore().CommitForget(scope, r.PathValue("entryID"), request.ReviewToken, request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"forgotten": r.PathValue("entryID"), "replay": replay})
}
