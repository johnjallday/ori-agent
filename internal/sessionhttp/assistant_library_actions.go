package sessionhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

const maxLibraryActionBytes = 32 << 10

// Strictly reject unknown, duplicate and trailing JSON, including duplicate
// keys inside a nested patch. The request never contains filesystem paths.
func decodeLibraryAction(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLibraryActionBytes+1))
	if err != nil || len(body) > maxLibraryActionBytes || len(body) == 0 {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	tokens := json.NewDecoder(bytes.NewReader(body))
	if err := uniqueLibraryJSON(tokens); err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	if _, err := tokens.Token(); !errors.Is(err, io.EOF) {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		_ = orihttp.RespondBadRequest(w, "Invalid library request")
		return false
	}
	return true
}

func uniqueLibraryJSON(decoder *json.Decoder) error {
	return libraryJSONValue(decoder, true)
}

func libraryJSONValue(decoder *json.Decoder, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	opening, ok := token.(json.Delim)
	if !ok {
		if root {
			return errors.New("expected JSON object")
		}
		return nil
	}
	if root && opening != '{' {
		return errors.New("expected JSON object")
	}
	switch opening {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			next, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := next.(string)
			if !ok || seen[name] {
				return errors.New("duplicate JSON key")
			}
			seen[name] = true
			if err := libraryJSONValue(decoder, false); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := libraryJSONValue(decoder, false); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func emptyLibraryAction(w http.ResponseWriter, r *http.Request) bool {
	var request struct{}
	return decodeLibraryAction(w, r, &request)
}

func (h *Handler) libraryEntryExists(w http.ResponseWriter, scope projectlibrary.Scope, entryID string) bool {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		respondLibraryActionError(w, err)
		return false
	}
	for _, entry := range doc.Entries {
		if entry.ID == entryID {
			return true
		}
	}
	_ = orihttp.RespondNotFound(w, "Library project not found")
	return false
}

// An ID from another Home is indistinguishable from an absent local ID.
func (h *Handler) libraryRootExists(w http.ResponseWriter, scope projectlibrary.Scope, rootID string) bool {
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		respondLibraryActionError(w, err)
		return false
	}
	for _, root := range doc.Roots {
		if root.ID == rootID {
			return true
		}
	}
	_ = orihttp.RespondNotFound(w, "Library root not found")
	return false
}

func respondLibraryActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projectlibrary.ErrConflict), errors.Is(err, projectlibrary.ErrNotInitialized):
		_ = orihttp.RespondConflict(w, "Project library changed; review again")
	case errors.Is(err, projectlibrary.ErrUnavailable):
		_ = orihttp.RespondConflict(w, "Project library or native picker is unavailable")
	case errors.Is(err, projectlibrary.ErrLimit):
		_ = orihttp.RespondBadRequest(w, "Project library limit exceeded")
	default:
		_ = orihttp.RespondInternalError(w, "Project library cannot be updated")
	}
}

type libraryCommitRequest struct {
	ReviewToken    string `json:"review_token"`
	IdempotencyKey string `json:"idempotency_key"`
	Confirm        bool   `json:"confirm"`
}

func (h *Handler) ReviewAssistantLibraryInitialize(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !emptyLibraryAction(w, r) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewInitialize(scope)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryInitialize(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed library initialization")
		return
	}
	doc, replay, err := h.assistantLibraryStore().CommitInitialize(scope, request.ReviewToken, request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"revision": doc.Revision, "linked_count": len(doc.Entries), "replay": replay})
}

func (h *Handler) PickAssistantLibraryRoot(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !emptyLibraryAction(w, r) {
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	token, err := h.assistantLibraryRoots.Pick(r.Context(), scope)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]string{"selection_token": token})
}

type libraryOfferPickRequest struct {
	OfferID string `json:"offer_id"`
}

func (h *Handler) PickAssistantLibraryOfferRoot(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryOfferPickRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if h.assistantLibraryRoots == nil || h.assistantLibraryOfferResolver == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	token, err := h.assistantLibraryRoots.PickFromPortfolio(r.Context(), scope, request.OfferID,
		h.assistantLibraryOfferResolver)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]string{"selection_token": token})
}

type libraryRootReviewRequest struct {
	SelectionToken string `json:"selection_token"`
	IfRevision     int64  `json:"if_revision"`
}

func (h *Handler) ReviewAssistantLibraryRoot(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryRootReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	review, err := h.assistantLibraryRoots.Review(scope, request.SelectionToken, request.IfRevision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryRoot(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed root")
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	root, replay, err := h.assistantLibraryRoots.Commit(scope, request.ReviewToken, request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"root_id": root.ID, "revision": root.Revision, "replay": replay})
}

type libraryScanReviewRequest struct {
	IfRevision int64  `json:"if_revision"`
	ScopeID    string `json:"scope_id,omitempty"`
}

type libraryScanCommitRequest struct {
	libraryCommitRequest
	ScopeID string `json:"scope_id,omitempty"`
}

func (h *Handler) ReviewAssistantLibraryScan(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryScanReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	rootID := r.PathValue("rootID")
	if !h.libraryRootExists(w, scope, rootID) {
		return
	}
	var review projectlibrary.ScanReview
	var err error
	if request.ScopeID == "" {
		review, err = h.assistantLibraryRoots.ReviewScan(scope, rootID, request.IfRevision)
	} else {
		review, err = h.assistantLibraryRoots.ReviewScopedScan(scope, rootID, request.ScopeID, request.IfRevision)
	}
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryScan(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryScanCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm this metadata-only scan")
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	rootID := r.PathValue("rootID")
	if !h.libraryRootExists(w, scope, rootID) {
		return
	}
	var scan projectlibrary.Scan
	var replay bool
	var err error
	if request.ScopeID == "" {
		scan, replay, err = h.assistantLibraryRoots.CommitScan(r.Context(), scope, rootID, request.ReviewToken, request.IdempotencyKey)
	} else {
		scan, replay, err = h.assistantLibraryRoots.CommitScopedScan(r.Context(), scope, rootID, request.ScopeID, request.ReviewToken, request.IdempotencyKey)
	}
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"scan_id": scan.ID, "root_id": scan.RootID,
		"status": scan.Status, "entries_seen": scan.EntriesSeen,
		"skipped_links": scan.SkippedLinks, "skipped_other": scan.SkippedOther,
		"partial_reason": scan.PartialReason, "known_scope_count": len(scan.KnownScopes), "replay": replay})
}

type libraryFieldsReviewRequest struct {
	IfFieldsRevision int64                      `json:"if_fields_revision"`
	Patch            projectlibrary.FieldsPatch `json:"patch"`
}

type libraryFieldsCommitRequest struct {
	libraryCommitRequest
	IfFieldsRevision int64                      `json:"if_fields_revision"`
	Patch            projectlibrary.FieldsPatch `json:"patch"`
}

func (h *Handler) ReviewAssistantLibraryFields(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryFieldsReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	entryID := r.PathValue("entryID")
	if !h.libraryEntryExists(w, scope, entryID) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewFields(scope, entryID, request.IfFieldsRevision, request.Patch, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryFields(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryFieldsCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed metadata changes")
		return
	}
	entryID := r.PathValue("entryID")
	if !h.libraryEntryExists(w, scope, entryID) {
		return
	}
	entry, replay, err := h.assistantLibraryStore().CommitFields(scope, entryID,
		request.ReviewToken, request.IdempotencyKey, request.IfFieldsRevision, request.Patch, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"entry_id": entry.ID, "fields": entry.Fields, "replay": replay})
}

type libraryRevisionRequest struct {
	IfRevision int64 `json:"if_revision"`
}

func (h *Handler) ReviewAssistantLibraryRevoke(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryRevisionRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	if !h.libraryRootExists(w, scope, r.PathValue("rootID")) {
		return
	}
	review, err := h.assistantLibraryRoots.ReviewRevoke(scope, r.PathValue("rootID"), request.IfRevision)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryRevoke(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm disconnection of this root")
		return
	}
	if h.assistantLibraryRoots == nil {
		respondLibraryActionError(w, projectlibrary.ErrUnavailable)
		return
	}
	if !h.libraryRootExists(w, scope, r.PathValue("rootID")) {
		return
	}
	root, replay, err := h.assistantLibraryRoots.CommitRevoke(scope, r.PathValue("rootID"), request.ReviewToken, request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"root_id": root.ID, "revision": root.Revision, "replay": replay})
}
