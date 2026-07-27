package connectionshttp

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// ProductTeardown removes a product's local footprint deterministically, without
// touching other products (FR 78, 79, 80). Implemented by a server-layer adapter
// over the MCP registry and the vault; nil disables teardown (the grant is still
// dropped, but credentials/bindings are left for a later sweep).
type ProductTeardown interface {
	// DisconnectProduct revokes a product's local credentials: the MCP server's
	// OAuth for Calendar/Drive (the server definition stays, so it is
	// reconnectable), the vault email account for Gmail (FR 79). Workspaces keep
	// their bindings and simply go to "Connection required" (FR 80).
	DisconnectProduct(ctx context.Context, product connections.ProductKey, credentialRef string) error
	// UnlinkProductFromWorkspace removes a single workspace's use of the product
	// (its MCP binding, or its linked email account) while the global grant and
	// every other workspace are left intact (FR 78).
	UnlinkProductFromWorkspace(ctx context.Context, product connections.ProductKey, credentialRef, workspaceID string) error
}

func knownProduct(p connections.ProductKey) bool {
	return slices.Contains(connections.AllProducts(), p)
}

// productDisconnect serves POST /api/connections/google/product/disconnect?product=X.
// It is a fully local operation (FR 79): it tears down that product's credentials,
// drops only its grant, and leaves the identity and every other grant untouched.
func (h *Handler) productDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	product := connections.ProductKey(strings.TrimSpace(r.URL.Query().Get("product")))
	if !knownProduct(product) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown_product", "message": "Unknown product."})
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	if conn == nil || !conn.HasVerifiedIdentity() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no_identity", "message": "No Google account is connected."})
		return
	}
	grant, ok := conn.Grant(product)
	if !ok || grant == nil {
		// Idempotent: nothing to disconnect. Return the current projection.
		writeJSON(w, http.StatusOK, connections.Project(conn))
		return
	}
	// Tear down credentials first (best-effort/deterministic), then drop the
	// grant. A teardown error does not strand the grant — the local grant is
	// still removed so the account never shows a product it can't use.
	if h.teardown != nil {
		if tErr := h.teardown.DisconnectProduct(r.Context(), product, grant.CredentialRef); tErr != nil {
			// Logged upstream by the adapter; proceed to drop the grant.
			_ = tErr
		}
	}
	conn.DisableGrant(product)
	if err := h.store.Save(conn); err != nil {
		http.Error(w, "failed to save connection", http.StatusInternalServerError)
		return
	}
	// Record withdrawal of this product's consent (FR 96).
	h.recordConsent(conn)
	writeJSON(w, http.StatusOK, connections.Project(conn))
}

// productUnlink serves POST /api/connections/google/product/unlink?product=X&workspace_id=Y.
// It removes just that workspace's use of the product; the global grant and all
// other workspaces are untouched (FR 78).
func (h *Handler) productUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	product := connections.ProductKey(strings.TrimSpace(r.URL.Query().Get("product")))
	if !knownProduct(product) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown_product", "message": "Unknown product."})
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_workspace", "message": "A workspace id is required."})
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	var credentialRef string
	if conn != nil {
		if g, ok := conn.Grant(product); ok && g != nil {
			credentialRef = g.CredentialRef
		}
	}
	if h.teardown == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Workspace unlink isn't available in this build."})
		return
	}
	if err := h.teardown.UnlinkProductFromWorkspace(r.Context(), product, credentialRef, workspaceID); err != nil {
		http.Error(w, "failed to unlink product from workspace", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}
