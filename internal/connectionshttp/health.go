package connectionshttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// GrantHealthChecker reports a product grant's live health WITHOUT any browser
// interaction (FR 85): Calendar/Drive map their MCP server's status. ok=false
// means "not determinable here" (Gmail, a transient state, or no such server) so
// the stored health is kept. A nil checker disables reconciliation.
type GrantHealthChecker interface {
	LiveHealth(product connections.ProductKey, credentialRef string) (health connections.GrantHealth, ok bool)
}

// HealthNotifier is called when a grant's health transitions during
// reconciliation, so the host can proactively surface an unhealthy grant via the
// event bus + Action Center and clear it on recovery (FR 86). Nil disables
// surfacing.
type HealthNotifier interface {
	OnGrantHealthChanged(ctx context.Context, product connections.ProductKey, credentialRef string, health connections.GrantHealth)
}

// reconcileGrantHealth refreshes each product grant's health from its live source
// before the status is projected, so a grant whose credential has gone stale
// surfaces as "Reconnect required" (or "Rate limited") without ever opening a
// browser (FR 85). On each transition it notifies the HealthNotifier so the host
// can raise/clear a proactive finding (FR 86). It returns whether anything
// changed, so the caller can persist the update. It never opens a browser or
// triggers an OAuth flow — it only reads existing runtime state.
func (h *Handler) reconcileGrantHealth(ctx context.Context, conn *connections.Connection) bool {
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
			if h.healthNotifier != nil {
				h.healthNotifier.OnGrantHealthChanged(ctx, product, g.CredentialRef, live)
			}
		}
	}
	return changed
}
