package personalhqhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/mailbox"
)

// ReplyService is the confirm-gated reply/send surface, implemented in the
// server package over the mailbox broker. Kept as an interface so this handler
// stays thin and testable, and so all sends funnel through one broker.
type ReplyService interface {
	DraftReply(ctx context.Context, userID, threadID, body string) (*mailbox.ReplyProposal, error)
	GetProposal(userID, id string) (*mailbox.ReplyProposal, error)
	ListProposals(userID string) []*mailbox.ReplyProposal
	EditProposal(ctx context.Context, userID, id string, to []string, subject, body string) (*mailbox.ReplyProposal, error)
	CancelProposal(userID, id string) error
	ConfirmSend(ctx context.Context, userID, id, expectedHash string) (*mailbox.ReplyProposal, error)
}

// SetReplyService wires the reply/send broker surface.
func (h *Handler) SetReplyService(svc ReplyService) { h.replies = svc }

// DraftReply handles POST /api/personal-hq/mail/draft: compose a LOCAL reply
// proposal from a source thread. Never sends.
func (h *Handler) DraftReply(w http.ResponseWriter, r *http.Request) {
	if !h.replyReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ThreadID string `json:"thread_id"`
		Body     string `json:"body"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.ThreadID == "" {
		orihttp.BadRequest(w, "thread_id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	p, err := h.replies.DraftReply(r.Context(), userID, req.ThreadID, req.Body)
	if err != nil {
		respondReplyError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"proposal": p})
}

// ListProposals handles GET /api/personal-hq/mail/proposals.
func (h *Handler) ListProposals(w http.ResponseWriter, r *http.Request) {
	if !h.replyReady(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	orihttp.Success(w, map[string]any{"proposals": h.replies.ListProposals(userID)})
}

// EditReply handles POST /api/personal-hq/mail/edit: replace a draft's editable
// fields, invalidating any prior confirmation.
func (h *Handler) EditReply(w http.ResponseWriter, r *http.Request) {
	if !h.replyReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID      string   `json:"id"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.ID == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	p, err := h.replies.EditProposal(r.Context(), userID, req.ID, req.To, req.Subject, req.Body)
	if err != nil {
		respondReplyError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"proposal": p})
}

// CancelReply handles POST /api/personal-hq/mail/cancel.
func (h *Handler) CancelReply(w http.ResponseWriter, r *http.Request) {
	if !h.replyReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	if err := h.replies.CancelProposal(userID, req.ID); err != nil {
		respondReplyError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"ok": true})
}

// ConfirmSend handles POST /api/personal-hq/mail/confirm: the ONLY send path.
// expected_hash binds the send to the exact payload the user reviewed.
func (h *Handler) ConfirmSend(w http.ResponseWriter, r *http.Request) {
	if !h.replyReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID           string `json:"id"`
		ExpectedHash string `json:"expected_hash"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.ID == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	p, err := h.replies.ConfirmSend(r.Context(), userID, req.ID, req.ExpectedHash)
	if err != nil {
		respondReplyError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"proposal": p})
}

func (h *Handler) replyReady(w http.ResponseWriter, r *http.Request, method string) bool {
	if !orihttp.RequireMethod(w, r, method) {
		return false
	}
	if h == nil || h.replies == nil {
		orihttp.ServiceUnavailable(w, "personal hq email replies are unavailable")
		return false
	}
	return true
}

func respondReplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mailbox.ErrProposalNotFound):
		orihttp.NotFound(w, "That reply draft was not found")
	case errors.Is(err, mailbox.ErrSendUnauthorized):
		orihttp.Forbidden(w, "Sending is not authorized — reconnect your email with send permission")
	case errors.Is(err, mailbox.ErrProposalExpired):
		orihttp.Conflict(w, "This draft expired — start a new reply")
	case errors.Is(err, mailbox.ErrPayloadChanged):
		orihttp.Conflict(w, "The reply changed since you reviewed it — review and confirm again")
	case errors.Is(err, mailbox.ErrProposalNotDraft):
		orihttp.Conflict(w, "This reply can no longer be sent")
	default:
		orihttp.BadRequest(w, err.Error())
	}
}
