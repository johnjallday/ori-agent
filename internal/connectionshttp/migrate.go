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
	// MigrateAccount re-points the legacy account's workspaces at the unified
	// connection's credential (no re-auth) and removes the legacy record — but
	// only when it can PROVE the record is a redundant copy. It requires the
	// account's email to match the connected account, else
	// connections.ErrAccountMismatch, and returns ErrNotProven when the record
	// was deliberately preserved.
	MigrateAccount(ctx context.Context, accountID, connectedEmail, credentialRef, vaultID string) error
}

// ErrNotProven means a legacy record could not be proven to be a redundant copy
// and was therefore left in place. Implementations return an error matching this
// so the endpoint can report "safely skipped" rather than a failure — preserving
// a credential is the correct outcome, not a fault.
var ErrNotProven = errors.New("connectionshttp: account could not be proven to be a duplicate")

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
		switch {
		case errors.Is(err, connections.ErrAccountMismatch):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "account_mismatch", "message": "That account belongs to a different Google account, so it can't be migrated."})
		case errors.Is(err, ErrNotProven):
			// Not a failure: Ori could not prove the record is redundant, so it
			// kept it. Deleting an in-use credential is the outcome worth avoiding.
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "skipped",
				"message": "Ori couldn't confirm this account is a duplicate of your connected account, so it was left in place. Nothing was deleted.",
			})
		default:
			http.Error(w, "failed to migrate the account", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "migrated"})
}
