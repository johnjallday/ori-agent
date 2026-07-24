package connectionshttp

import "github.com/johnjallday/ori-agent/internal/connections"

// GrantHealthChecker reports a product grant's live health WITHOUT any browser
// interaction (FR 85): Calendar/Drive map their MCP server's status. ok=false
// means "not determinable here" (Gmail, a transient state, or no such server) so
// the stored health is kept. A nil checker disables reconciliation.
type GrantHealthChecker interface {
	LiveHealth(product connections.ProductKey, credentialRef string) (health connections.GrantHealth, ok bool)
}

// reconcileGrantHealth refreshes each product grant's health from its live source
// before the status is projected, so a grant whose credential has gone stale
// surfaces as "Reconnect required" (or "Rate limited") without ever opening a
// browser (FR 85). It returns whether anything changed, so the caller can persist
// the update. It never opens a browser or triggers an OAuth flow — it only reads
// existing runtime state.
func (h *Handler) reconcileGrantHealth(conn *connections.Connection) bool {
	if conn == nil || h.health == nil {
		return false
	}
	changed := false
	for _, product := range connections.AllProducts() {
		g, ok := conn.Grant(product)
		if !ok || g == nil {
			continue
		}
		live, ok := h.health.LiveHealth(product, g.CredentialRef)
		if ok && live != g.Health {
			conn.SetGrantHealth(product, live)
			changed = true
		}
	}
	return changed
}
