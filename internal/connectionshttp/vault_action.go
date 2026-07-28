package connectionshttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// Browser-facing vocabulary for a vault step that must happen before product
// authorization can start (FR 5-11). The payload is deliberately small and
// token-free: vault ids and display names only, never a path, password, key, or
// record count.

// vaultActionResponse tells the connection card exactly which repair to offer
// and lets it resume the pending Gmail enable afterward.
type vaultActionResponse struct {
	// Error is the stable machine code the client switches on.
	Error string `json:"error"`
	// Action is the specific next step: create, choose, unlock, or repair.
	Action string `json:"action"`
	// Message is the safe, action-specific sentence to display.
	Message string `json:"message"`
	// VaultID/VaultName identify the vault an unlock or repair applies to.
	VaultID   string `json:"vault_id,omitempty"`
	VaultName string `json:"vault_name,omitempty"`
	// Vaults are the selectable vaults for choose/repair, so the client never has
	// to make a second call to render the prompt.
	Vaults []vaultOptionResponse `json:"vaults,omitempty"`
}

type vaultOptionResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Locked bool   `json:"locked"`
}

// vaultActionErrorCode is the stable machine code for every vault-blocked
// authorization. Clients switch on Action for the specific step.
const vaultActionErrorCode = "vault_action_required"

// vaultActionMessages are the user-facing sentences per outcome. Each names the
// action the user must take, because "we couldn't complete the Google sign-in"
// was exactly the unhelpful message this work exists to remove.
var vaultActionMessages = map[connections.VaultOutcome]string{
	connections.VaultOutcomeCreate: "Create a vault to store your Google credentials, then Ori will continue enabling Gmail.",
	connections.VaultOutcomeChoose: "Choose which vault should store your Google credentials.",
	connections.VaultOutcomeUnlock: "Unlock the vault that stores your Google credentials, then Ori will continue enabling Gmail.",
	connections.VaultOutcomeRepair: "The vault Ori remembered for your Google credentials is unavailable. Choose another vault or create a new one.",
}

// vaultPreflight answers "which vault step is needed right now?" WITHOUT any
// side effect — no OAuth state is created and no vault choice is recorded. The
// Google Account card uses it to re-open the right prompt after a callback that
// failed on a local vault step (FR 13, 14, 18).
func (h *Handler) vaultPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.vaults == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Vaults aren't available in this build."})
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	savedVaultID := ""
	if conn != nil {
		savedVaultID = conn.VaultID
	}
	preflight, err := connections.PreflightVault(r.Context(), h.vaults, savedVaultID)
	if err != nil {
		http.Error(w, "failed to resolve vault", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, vaultActionPayload(preflight))
}

// vaultActionPayload projects a preflight into its browser response. A ready
// preflight uses the same shape with action "ready" and no message.
func vaultActionPayload(p connections.VaultPreflight) vaultActionResponse {
	options := make([]vaultOptionResponse, 0, len(p.Options))
	for _, v := range p.Options {
		options = append(options, vaultOptionResponse{
			ID:     v.ID,
			Name:   v.Name,
			Locked: v.Availability == connections.VaultLocked,
		})
	}
	message := vaultActionMessages[p.Outcome]
	if message == "" && p.Outcome != connections.VaultOutcomeReady {
		message = "Ori needs a vault to store your Google credentials."
	}
	return vaultActionResponse{
		Error:     vaultActionErrorCode,
		Action:    string(p.Outcome),
		Message:   message,
		VaultID:   p.VaultID,
		VaultName: p.VaultName,
		Vaults:    options,
	}
}
