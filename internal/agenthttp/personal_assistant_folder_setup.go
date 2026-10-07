package agenthttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type PersonalAssistantFolderSetups interface {
	ReviewOptions(context.Context, foldercontext.Target, string) []personalassistant.FolderReviewOption
	Review(context.Context, foldercontext.Target, string, string, string) (personalassistant.FolderOfferView, error)
	CloseReview(context.Context, foldercontext.Target, string) error
	ReadReview(context.Context, foldercontext.Target, string) (*personalassistant.FolderOfferView, error)
	ConfigureConversationReviews(func(context.Context, personalassistant.FolderOffer) error, func(foldercontext.Target) (func(), bool))
}

func (h *HomeAssistantAskHandler) SetFolderSetups(service PersonalAssistantFolderSetups) {
	h.FolderSetups = service
	if service != nil {
		service.ConfigureConversationReviews(h.validateFolderReview, h.folderRequests.enter)
	}
}

func (h *HomeAssistantAskHandler) validateFolderReview(ctx context.Context, offer personalassistant.FolderOffer) error {
	review := offer.ConversationReview
	if review == nil {
		return foldercontext.ErrInvalid
	}
	work, err := h.resolvePersonalAssistantContext(ctx)
	scope, ready := conversationScope(work)
	if err != nil || !ready {
		return foldercontext.ErrInvalid
	}
	target, err := h.folderTarget(scope, review.Target.ConversationID, "")
	if err != nil || target != review.Target {
		return foldercontext.ErrInvalid
	}
	store := h.folderStore()
	if store == nil {
		return foldercontext.ErrInvalid
	}
	record, messages, err := store.ReadFolderConversation(ctx, target.ConversationID)
	if err != nil || !scope.owns(record) {
		return foldercontext.ErrInvalid
	}
	// Confirmed work can finish after detach, but not after deletion, import or
	// an ownership change. Its exact local canonical review must still exist.
	if offer.DecidedAt != nil {
		for _, message := range messages {
			if !message.Imported && message.FolderContext != nil && message.FolderContext.OfferID == offer.ID &&
				message.FolderContext.Observation != nil && message.FolderContext.Observation.ID == review.ObservationID {
				return nil
			}
		}
		return foldercontext.ErrInvalid
	}
	state := folderStateFromMessages(messages)
	if state.Observation == nil || state.Observation.ID != review.ObservationID || state.OfferID != offer.ID {
		return foldercontext.ErrInvalid
	}
	return nil
}

func writeFolderReviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, personalassistant.ErrFolderOfferDecided) {
		writeConversationError(w, http.StatusConflict, "folder_review_declined", "Setup suggestions for this folder were previously declined. You can keep chatting or use New Workspace separately.")
		return
	}
	if errors.Is(err, personalassistant.ErrFolderReviewPending) {
		writeConversationError(w, http.StatusConflict, "folder_review_pending", err.Error())
		return
	}
	writeFolderError(w, err)
}

// ReviewFolderSetupHandler is an explicit local review, not a chat approval or
// model call. It may save the first observation without requiring an LLM.
func (h *HomeAssistantAskHandler) ReviewFolderSetupHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if h.FolderSetups == nil || h.FolderObservations == nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	var req struct {
		ConversationID string                     `json:"conversation_id,omitempty"`
		DraftID        string                     `json:"draft_id,omitempty"`
		Revision       string                     `json:"revision"`
		SelectionID    string                     `json:"selection_id"`
		CandidateID    string                     `json:"candidate_id"`
		Context        *HomeAssistantRouteContext `json:"context,omitempty"`
		Operation      string                     `json:"operation,omitempty"`
		DestinationID  string                     `json:"destination_id,omitempty"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	target, err := h.folderTarget(scope, req.ConversationID, req.DraftID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	state, err := h.folderExpected(r.Context(), target, req.Revision)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	placementContext, placementReady := h.resolveFolderReviewPlacement(w, r, target, req.Context, req.Operation, req.DestinationID, req.SelectionID, req.CandidateID, state.OfferID)
	if !placementReady {
		return
	}
	observation, err := h.resolveFolderObservation(r.Context(), target, req.SelectionID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	if observation == nil || observation.Validate() != nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	id := req.ConversationID
	created := false
	if id == "" {
		record, err := h.Conversations.Create(r.Context(), scope.workspaceID, scope.agentName, conversationTitle("Workspace setup: "+observation.Folder))
		if err != nil {
			writeFolderError(w, err)
			return
		}
		id, created = record.ID, true
	}
	savedTarget := target
	savedTarget.ConversationID, savedTarget.DraftID = id, ""
	offer, err := h.FolderSetups.Review(placementContext, target, id, req.SelectionID, req.CandidateID)
	if err != nil {
		if created {
			_ = h.Conversations.Discard(r.Context(), id)
		}
		writeFolderReviewError(w, err)
		return
	}
	// No controls are live until this exact ID exists in canonical history and
	// the staged selection is bound. Failed writes cannot grant a partial offer.
	fresh, scopeOK := h.readScope(w, r)
	if !scopeOK || fresh != scope {
		_ = h.FolderSetups.CloseReview(r.Context(), savedTarget, offer.ID)
		if created {
			_ = h.Conversations.Discard(r.Context(), id)
		}
		if scopeOK {
			writeFolderError(w, foldercontext.ErrInvalid)
		}
		return
	}
	rows, err := h.folderStore().AppendFolderTurn(r.Context(), id, scope.workspaceID, scope.agentName, req.Revision,
		foldercontext.Event{Version: 1, Observation: observation, OfferID: offer.ID}, "", "")
	if err != nil {
		_ = h.FolderSetups.CloseReview(r.Context(), savedTarget, offer.ID)
		if created {
			_ = h.Conversations.Discard(r.Context(), id)
		}
		writeFolderError(w, err)
		return
	}
	h.FolderObservations.BindSaved(target, observation.ID, id)
	state = PersonalAssistantFolderState{Revision: rows[0].ID, Observation: observation, OfferID: offer.ID}
	orihttp.WriteJSON(w, map[string]any{
		"conversation":   HomeAssistantConversationState{ID: id, Stored: true, Started: created},
		"folder_context": state,
	})
}

// CloseFolderReviewHandler retires an unconfirmed review without No thanks,
// rescanning, a permanent preference, or undoing a confirmed setup.
func (h *HomeAssistantAskHandler) CloseFolderReviewHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id"`
		Revision       string `json:"revision"`
		OfferID        string `json:"offer_id"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	target, err := h.folderTarget(scope, req.ConversationID, "")
	if err != nil || h.FolderSetups == nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	if req.OfferID == "" {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	offer, err := h.FolderSetups.ReadReview(r.Context(), target, req.OfferID)
	if err != nil || offer == nil || offer.Status != personalassistant.FolderOfferPending {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	state, err := h.folderExpected(r.Context(), target, req.Revision)
	if err != nil {
		// Deleting/moving a conversation must not strand its pending sidecar and
		// block every future review. This explicit closure still requires the
		// original user/HQ/profile provenance checked by ReadReview above. Read
		// failures are not evidence of deletion, and no foreign Session is edited.
		record, _, readErr := h.folderStore().ReadFolderConversation(r.Context(), target.ConversationID)
		orphaned := errors.Is(readErr, ErrPersonalAssistantConversationNotFound) || (readErr == nil && !scope.owns(record))
		if !orphaned {
			writeFolderError(w, err)
			return
		}
		if err := h.FolderSetups.CloseReview(r.Context(), target, req.OfferID); err != nil {
			writeFolderError(w, err)
			return
		}
		orihttp.WriteJSON(w, map[string]any{"review_closed": true})
		return
	}
	if state.OfferID == req.OfferID {
		rows, err := h.folderStore().AppendFolderTurn(r.Context(), target.ConversationID, target.WorkspaceID, target.AgentName, req.Revision,
			foldercontext.Event{Version: 1, Observation: state.Observation}, "", "")
		if err != nil {
			writeFolderError(w, err)
			return
		}
		state.Revision, state.OfferID = rows[0].ID, ""
	}
	// Canonical retirement comes first: even if sidecar storage fails, no stale
	// review can execute. A later explicit review can replace this same-thread row.
	if err := h.FolderSetups.CloseReview(r.Context(), target, req.OfferID); err != nil {
		writeFolderError(w, err)
		return
	}
	state.Historical = state.Observation != nil // authority is refreshed by canonical hydration, not inferred here
	orihttp.WriteJSON(w, map[string]any{"conversation": HomeAssistantConversationState{ID: target.ConversationID, Stored: true}, "folder_context": state})
}

func (h *HomeAssistantAskHandler) folderReviewViews(ctx context.Context, target foldercontext.Target, messages []PersonalAssistantConversationMessage) map[string]*personalassistant.FolderOfferView {
	views := make(map[string]*personalassistant.FolderOfferView)
	if h.FolderSetups == nil {
		return views
	}
	// Bound extra read/projection work to the sixteen most recent unique refs.
	seen := make(map[string]bool)
	for i := len(messages) - 1; i >= 0 && len(seen) < 16; i-- {
		message := messages[i]
		if message.Imported || message.FolderContext == nil || message.FolderContext.OfferID == "" {
			continue
		}
		id := message.FolderContext.OfferID
		if seen[id] {
			continue
		}
		seen[id] = true
		view, err := h.FolderSetups.ReadReview(ctx, target, id)
		if err == nil {
			views[id] = view
		}
	}
	return views
}
