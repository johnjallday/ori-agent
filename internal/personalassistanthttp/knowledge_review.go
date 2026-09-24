package personalassistanthttp

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (h *Handler) SetKnowledgeReview(service *personalassistant.KnowledgeLearningService) {
	if h != nil {
		h.knowledgeReview = service
	}
}

// GetKnowledgeReview is observational: it neither checks for proposals nor
// changes lifecycle state. The domain projection reads exact current canonical
// bytes before presenting anything as an approved fact.
func (h *Handler) GetKnowledgeReview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.knowledgeReview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ review is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	items, err := h.knowledgeReview.ReviewItems(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	version, err := h.knowledgeReview.CurrentStateVersion(r.Context(), userID)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	if items == nil {
		items = []personalassistant.KnowledgeReviewItem{}
	}
	sources := KnowledgeSources{
		SavedApps:   KnowledgeSourceCard{Status: "unavailable"},
		FileJanitor: KnowledgeSourceCard{Status: "unavailable"},
	}
	if h.knowledgeSources != nil {
		sources = h.knowledgeSources.ReadSources(r.Context(), userID)
	}
	orihttp.Success(w, map[string]any{"items": items, "state_version": version, "sources": sources})
}

type knowledgeExplicitRequest struct {
	StateVersion int64  `json:"state_version"`
	RequestID    string `json:"request_id"`
	Category     string `json:"category"`
	Text         string `json:"text"`
}

// SaveExplicitKnowledge is a user-confirmed HQ fact, never an autonomous
// candidate seeding endpoint. Scope, canonical destination and source identity
// are selected by the server; the browser supplies only reviewed wording,
// category, relationship version and a single-action retry key.
func (h *Handler) SaveExplicitKnowledge(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.knowledgeReview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ facts are unavailable")
		return
	}
	var body knowledgeExplicitRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil || body.StateVersion < 1 || !validReviewRequestID(body.RequestID) {
		orihttp.BadRequest(w, "Review the fact and current assistant state before saving")
		return
	}
	clean, err := workspace.ValidateMemoryText(body.Text)
	if err != nil || clean != body.Text {
		orihttp.BadRequest(w, "Use one safe, exact line of reviewed text")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	saved, err := h.knowledgeReview.SaveExplicit(r.Context(), userID, body.StateVersion, body.RequestID, body.Category, body.Text)
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	items, err := h.knowledgeReview.ReviewItems(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "The fact was saved. Refresh to check its current value")
		return
	}
	for _, item := range items {
		if item.ID == saved.ID && item.SourceKind == "explicit" && item.Text == body.Text && item.Category == body.Category && item.State == personalassistant.KnowledgeApproved {
			orihttp.Success(w, map[string]any{"item": item})
			return
		}
	}
	orihttp.ServiceUnavailable(w, "The fact was saved. Refresh to check its current value")
}

type knowledgeMutationRequest struct {
	Version   int64  `json:"version"`
	RequestID string `json:"request_id"`
}

type knowledgeEditRequest struct {
	Version   int64  `json:"version"`
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
}

type knowledgeReconfirmRequest struct {
	Version    int64  `json:"version"`
	RequestID  string `json:"request_id"`
	Text       string `json:"text"`
	RevisionID string `json:"revision_id"`
	Checkpoint string `json:"evidence_checkpoint"`
}

// MutateKnowledgeReview accepts only a server-identified item, an expected
// version, an idempotency key and (for an edit) exact user-reviewed text. There
// is deliberately no candidate-create, source/evidence, owner/HQ, destination
// or arbitrary field selector on this route.
func (h *Handler) MutateKnowledgeReview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.knowledgeReview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ review is unavailable")
		return
	}
	itemID := r.PathValue("item_id")
	if _, err := uuid.Parse(itemID); err != nil {
		orihttp.BadRequest(w, "Check the review item and try again")
		return
	}
	action := r.PathValue("action")
	var body knowledgeEditRequest
	var reconfirm knowledgeReconfirmRequest
	switch action {
	case "approve", "reject", "forget":
		var mutation knowledgeMutationRequest
		if err := decodeBoundedRequest(w, r, &mutation); err != nil {
			orihttp.BadRequest(w, "Check the review action and try again")
			return
		}
		body.Version, body.RequestID = mutation.Version, mutation.RequestID
	case "reconfirm":
		if err := decodeBoundedRequest(w, r, &reconfirm); err != nil {
			orihttp.BadRequest(w, "Check the evidence review and try again")
			return
		}
		body = knowledgeEditRequest{Version: reconfirm.Version, RequestID: reconfirm.RequestID, Text: reconfirm.Text}
		if len(reconfirm.Checkpoint) != 64 || len(reconfirm.RevisionID) > 100 || reconfirm.RevisionID == "" {
			orihttp.BadRequest(w, "Review the current evidence before reconfirming")
			return
		}
	case "edit-candidate", "edit-approved":
		if err := decodeBoundedRequest(w, r, &body); err != nil {
			orihttp.BadRequest(w, "Check the review edit and try again")
			return
		}
	default:
		orihttp.BadRequest(w, "Unsupported review action")
		return
	}
	if action == "reconfirm" || action == "edit-candidate" || action == "edit-approved" {
		clean, err := workspace.ValidateMemoryText(body.Text)
		if err != nil || clean != body.Text {
			orihttp.BadRequest(w, "Use one safe, exact line of reviewed text")
			return
		}
	}
	if body.Version < 1 || !validReviewRequestID(body.RequestID) {
		orihttp.BadRequest(w, "Check the item version and retry key")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var err error
	switch action {
	case "approve":
		_, err = h.knowledgeReview.ApproveCandidate(r.Context(), userID, itemID, body.Version, body.RequestID)
	case "reject":
		_, err = h.knowledgeReview.SuppressCandidateWithReceipt(r.Context(), userID, itemID, body.Version, body.RequestID, personalassistant.KnowledgeRejected)
	case "forget":
		_, err = h.knowledgeReview.ForgetItem(r.Context(), userID, itemID, body.Version, body.RequestID)
	case "edit-candidate":
		_, err = h.knowledgeReview.EditCandidateWithReceipt(r.Context(), userID, itemID, body.Version, body.RequestID, body.Text)
	case "edit-approved":
		_, err = h.knowledgeReview.EditApproved(r.Context(), userID, itemID, body.Version, body.RequestID, body.Text)
	case "reconfirm":
		_, err = h.knowledgeReview.Reconfirm(r.Context(), userID, itemID, body.Version, body.RequestID,
			reconfirm.RevisionID, reconfirm.Checkpoint, body.Text)
	}
	if err != nil {
		writeKnowledgeReviewError(w, err)
		return
	}
	items, err := h.knowledgeReview.ReviewItems(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "The review was saved. Reload to verify the current value")
		return
	}
	for _, item := range items {
		if item.ID == itemID {
			orihttp.Success(w, map[string]any{"item": item})
			return
		}
	}
	orihttp.Success(w, map[string]any{"id": itemID, "state": action})
}

func validReviewRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func writeKnowledgeReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrKnowledgeQuota), errors.Is(err, personalassistant.ErrKnowledgeLimit):
		orihttp.Conflict(w, "The review queue is full; resolve existing items before adding more")
	case errors.Is(err, personalassistant.ErrNeedsHQ):
		orihttp.Conflict(w, "Build Personal HQ before reviewing facts")
	case errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrNotFound),
		errors.Is(err, personalassistant.ErrRepairNeeded), errors.Is(err, personalassistant.ErrKnowledgePaused):
		orihttp.Conflict(w, "The review changed or needs repair. Refresh before continuing")
	default:
		orihttp.ServiceUnavailable(w, "Personal HQ review is temporarily unavailable")
	}
}
