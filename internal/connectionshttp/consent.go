package connectionshttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// recordConsent reconciles the consent audit log against the connection's current
// grants (FR 96): newly-enabled products record a grant, disabled/disconnected
// ones record a withdrawal. Best-effort — a logging failure never blocks the
// request. A nil conn withdraws any remaining active consent (whole-account
// disconnect).
func (h *Handler) recordConsent(conn *connections.Connection) {
	if h.consent == nil {
		return
	}
	if _, err := h.consent.Reconcile(conn); err != nil {
		logger.Warn("connection consent: reconcile failed", logger.Fields{"error": err})
	}
}

// consentAudit serves GET /api/connections/google/consent — the token/secret/
// content-free consent audit trail (FR 96), oldest-first.
func (h *Handler) consentAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records := []connections.ConsentRecord{}
	if h.consent != nil {
		got, err := h.consent.List()
		if err != nil {
			http.Error(w, "failed to load consent log", http.StatusInternalServerError)
			return
		}
		if got != nil {
			records = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}
