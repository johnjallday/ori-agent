package connectionshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// LegacyAccount is a pre-unified Gmail account (not sourced from the shared
// connection) the user can migrate onto their connected account. Content-free —
// no tokens (FR 88).
type LegacyAccount struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	EmailMatches bool   `json:"email_matches"`
}

// Migrator detects legacy Gmail accounts and folds one into the unified
// connection (FR 88/89). Nil disables migration.
type Migrator interface {
	// ListLegacyGmailAccounts returns Gmail accounts NOT sourced from the shared
	// connection (id/email/workspace only).
	ListLegacyGmailAccounts(ctx context.Context) ([]LegacyAccount, error)
	// MigrateAccount re-links the legacy account's workspace to the unified
	// connection (no re-auth, reusing the connection's Gmail credential) and
	// removes the legacy record. It requires the account's email to match the
	// connected account, else connections.ErrAccountMismatch.
	MigrateAccount(ctx context.Context, accountID, connectedEmail, credentialRef, vaultID string) error
}

// migratable serves GET /api/connections/google/migratable — legacy Gmail
// accounts eligible to move onto the connected account (FR 88/89). Empty unless a
// verified identity exists (nothing to migrate onto).
func (h *Handler) migratable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := []LegacyAccount{}
	if h.migrator != nil {
		if conn, err := h.store.Load(); err == nil && conn != nil && conn.HasVerifiedIdentity() {
			if accts, err := h.migrator.ListLegacyGmailAccounts(r.Context()); err == nil {
				for _, a := range accts {
					a.EmailMatches = strings.EqualFold(strings.TrimSpace(a.Email), strings.TrimSpace(conn.Email))
					out = append(out, a)
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// migrate serves POST /api/connections/google/migrate?account_id=X — folds a
// legacy account into the unified connection. A non-migrated account is left
// working untouched (FR 87); a different-account migration is rejected (FR 89).
func (h *Handler) migrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_account", "message": "An account id is required."})
		return
	}
	if h.migrator == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Migration isn't available in this build."})
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	g, ok := connGmailGrant(conn)
	if conn == nil || !conn.HasVerifiedIdentity() || !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "gmail_not_enabled", "message": "Connect your Google account and enable Gmail before migrating."})
		return
	}
	if err := h.migrator.MigrateAccount(r.Context(), accountID, conn.Email, g.CredentialRef, conn.VaultID); err != nil {
		if errors.Is(err, connections.ErrAccountMismatch) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "account_mismatch", "message": "That account belongs to a different Google account, so it can't be migrated."})
			return
		}
		http.Error(w, "failed to migrate the account", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "migrated"})
}
