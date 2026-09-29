package sessionhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// All session operations derive the exact Home and author from the authenticated
// user. No project filesystem, child agent, task, or DAW access is involved.
func (h *Handler) GetAssistantStudioResume(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid studio resume request")
		return
	}
	view, err := h.assistantLibraryStore().Resume(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

func (h *Handler) ListAssistantStudioSessions(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	if len(r.URL.RawQuery) > 256 {
		_ = orihttp.RespondBadRequest(w, "Invalid studio-session page")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid studio-session page")
		return
	}
	for name, values := range values {
		if (name != "revision" && name != "offset") || len(values) != 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid studio-session page")
			return
		}
	}
	var revision int64
	if raw := values.Get("revision"); raw != "" {
		revision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid studio-session page")
			return
		}
	}
	offset := 0
	if raw := values.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 4096 || revision == 0 {
			_ = orihttp.RespondBadRequest(w, "Invalid studio-session page")
			return
		}
	}
	page, err := h.assistantLibraryStore().ListSessions(scope, r.PathValue("entryID"), revision, offset)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}

func (h *Handler) GetAssistantStudioSession(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid studio-session request")
		return
	}
	record, err := h.assistantLibraryStore().GetSession(scope, r.PathValue("entryID"), r.PathValue("sessionID"))
	if err != nil {
		if errors.Is(err, projectlibrary.ErrConflict) {
			_ = orihttp.RespondNotFound(w, "Studio session not found")
		} else {
			respondLibraryReadError(w, err)
		}
		return
	}
	_ = orihttp.RespondSuccess(w, record)
}

type studioGoalReviewRequest struct {
	EntryRevision int64                    `json:"if_entry_revision"`
	Goal          projectlibrary.GoalInput `json:"goal"`
}

type studioGoalCommitRequest struct {
	studioGoalReviewRequest
	ReviewToken    string `json:"review_token"`
	IdempotencyKey string `json:"idempotency_key"`
	Confirm        bool   `json:"confirm"`
}

func (h *Handler) ReviewAssistantStudioGoal(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	var request studioGoalReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewGoal(scope, r.PathValue("entryID"), request.EntryRevision, request.Goal, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantStudioGoal(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	var request studioGoalCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed session goal")
		return
	}
	session, replay, err := h.assistantLibraryStore().CommitGoal(scope, r.PathValue("entryID"), request.ReviewToken,
		request.IdempotencyKey, request.EntryRevision, request.Goal, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"session": session, "replay": replay})
}

// Owner receipt choices are read only from this Home and its currently
// verified project link. No child Ticket content or status is opened.
func (h *Handler) ListAssistantStudioHandoffReceipts(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid handoff receipt request")
		return
	}
	list, err := h.assistantLibraryStore().HandoffsForOwner(scope, r.PathValue("entryID"))
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, list)
}

type studioRecapReviewRequest struct {
	SessionRevision int64                     `json:"if_session_revision"`
	FieldsRevision  int64                     `json:"if_fields_revision"`
	Recap           projectlibrary.RecapInput `json:"recap"`
}

type studioRecapCommitRequest struct {
	studioRecapReviewRequest
	ReviewToken    string `json:"review_token"`
	IdempotencyKey string `json:"idempotency_key"`
	Confirm        bool   `json:"confirm"`
}

func (h *Handler) librarySessionExists(w http.ResponseWriter, scope projectlibrary.Scope, entryID, sessionID string) bool {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return false
	}
	for _, session := range doc.Sessions {
		if session.ID == sessionID && session.EntryID == entryID {
			return true
		}
	}
	_ = orihttp.RespondNotFound(w, "Studio session not found")
	return false
}

func (h *Handler) ReviewAssistantStudioRecap(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) ||
		!h.librarySessionExists(w, scope, r.PathValue("entryID"), r.PathValue("sessionID")) {
		return
	}
	var request studioRecapReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewRecap(scope, r.PathValue("sessionID"),
		request.SessionRevision, request.FieldsRevision, request.Recap, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantStudioRecap(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) ||
		!h.librarySessionExists(w, scope, r.PathValue("entryID"), r.PathValue("sessionID")) {
		return
	}
	var request studioRecapCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed session recap")
		return
	}
	session, replay, err := h.assistantLibraryStore().CommitRecap(scope, r.PathValue("sessionID"),
		request.ReviewToken, request.IdempotencyKey, request.SessionRevision, request.FieldsRevision,
		request.Recap, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"session": session, "replay": replay})
}
