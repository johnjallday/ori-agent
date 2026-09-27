package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// An already-linked canonical project can be offered for separate, explicit
// Home catalog association. Reads do not adopt or scan its directory.
func (h *Handler) ListAssistantLibraryPendingLinks(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return
	}
	pending, err := h.assistantLibraryStore().PendingLinkedProjects(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, pending)
}

func (h *Handler) ReviewAssistantLibraryLinkedProject(w http.ResponseWriter, r *http.Request) {
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
	review, err := h.assistantLibraryStore().ReviewLinkedProject(scope, r.PathValue("projectID"), request.Revision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryLinkedProject(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed project association")
		return
	}
	result, err := h.assistantLibraryStore().CommitLinkedProject(scope, r.PathValue("projectID"), request.ReviewToken, request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, result)
}
