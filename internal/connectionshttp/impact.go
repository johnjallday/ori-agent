package connectionshttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// WorkspaceImpact names one workspace affected by disconnecting a product. It
// carries only safe identifiers — never a credential or token (FR 76).
type WorkspaceImpact struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ImpactEnumerator lists the workspaces that reference a product's grant so the
// UI can preview what a product- or whole-account disconnect will affect
// (FR 77, 79, 80). It resolves membership from safe references — the grant's MCP
// server name (Calendar/Drive) or its linked email account (Gmail) — and never
// reads credentials. A nil enumerator degrades to an empty preview.
type ImpactEnumerator interface {
	WorkspacesUsingProduct(ctx context.Context, product connections.ProductKey, credentialRef string) ([]WorkspaceImpact, error)
}

// productImpact is one product row of the disconnect impact preview.
type productImpact struct {
	Product    connections.ProductKey `json:"product"`
	Workspaces []WorkspaceImpact      `json:"workspaces"`
}

type impactResponse struct {
	Products []productImpact `json:"products"`
}

// impact serves GET /api/connections/google/impact — the disconnect impact
// preview. For every enabled product grant it reports the workspaces that would
// lose access, so a product- or whole-account disconnect is shown before it is
// confirmed (FR 77, 80). Reading it never mutates anything.
func (h *Handler) impact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	resp := impactResponse{Products: []productImpact{}}
	if conn == nil || !conn.HasVerifiedIdentity() {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Optional ?product= filter narrows the preview to a single product.
	only := connections.ProductKey(strings.TrimSpace(r.URL.Query().Get("product")))
	for _, product := range connections.AllProducts() {
		if only != "" && product != only {
			continue
		}
		grant, ok := conn.Grant(product)
		if !ok || grant == nil {
			continue
		}
		workspaces := []WorkspaceImpact{}
		if h.impacts != nil {
			found, encErr := h.impacts.WorkspacesUsingProduct(r.Context(), product, grant.CredentialRef)
			if encErr == nil {
				workspaces = found
			}
		}
		resp.Products = append(resp.Products, productImpact{Product: product, Workspaces: workspaces})
	}
	writeJSON(w, http.StatusOK, resp)
}
