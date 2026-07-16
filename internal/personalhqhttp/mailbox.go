package personalhqhttp

import (
	"context"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// MailboxStatus is the connection state of the Personal HQ's email account,
// surfaced to the connect/grant/repair UI (task 3.10).
type MailboxStatus struct {
	Connected    bool   `json:"connected"`
	AccountID    string `json:"account_id,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
	// Health mirrors mailbox.Health: healthy | disconnected | expired |
	// scope_upgrade. Empty when not connected.
	Health string `json:"health,omitempty"`
}

// MailboxLinker performs the Personal HQ email connect/disconnect operations
// against the designated HQ workspace. Implemented in the server package (it
// needs the workspace store, Vault, and the mailbox cache). Kept as an interface
// so this handler stays thin and testable.
type MailboxLinker interface {
	MailboxStatus(ctx context.Context, userID string) (MailboxStatus, error)
	LinkMailbox(ctx context.Context, userID, accountID string) (MailboxStatus, error)
	UnlinkMailbox(ctx context.Context, userID string) (MailboxStatus, error)
}

// SetMailboxLinker wires the email connect/disconnect operations.
func (h *Handler) SetMailboxLinker(linker MailboxLinker) { h.mailboxLinker = linker }

// MailboxStatus handles GET /api/personal-hq/email/status.
func (h *Handler) MailboxStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.mailboxLinker == nil {
		orihttp.ServiceUnavailable(w, "personal hq email is unavailable")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	status, err := h.mailboxLinker.MailboxStatus(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to load email status: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// LinkMailbox handles POST /api/personal-hq/email/link, attaching an already
// OAuth-connected account to the HQ so the Inbox specialist can read it.
func (h *Handler) LinkMailbox(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.mailboxLinker == nil {
		orihttp.ServiceUnavailable(w, "personal hq email is unavailable")
		return
	}
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.AccountID == "" {
		orihttp.BadRequest(w, "account_id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	status, err := h.mailboxLinker.LinkMailbox(r.Context(), userID, req.AccountID)
	if err != nil {
		respondMailboxError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// UnlinkMailbox handles POST /api/personal-hq/email/unlink.
func (h *Handler) UnlinkMailbox(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.mailboxLinker == nil {
		orihttp.ServiceUnavailable(w, "personal hq email is unavailable")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	status, err := h.mailboxLinker.UnlinkMailbox(r.Context(), userID)
	if err != nil {
		respondMailboxError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

func respondMailboxError(w http.ResponseWriter, err error) {
	// Linking errors are mostly client-actionable (no HQ, unknown account,
	// account scoped to another workspace); surface them as conflicts.
	orihttp.Conflict(w, err.Error())
}
