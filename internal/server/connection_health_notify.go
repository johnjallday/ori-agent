package server

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// connectionHealthNotifier surfaces a Google grant's health transition into the
// affected workspaces' Action Center (FR 86): an unhealthy grant raises a
// dedup-keyed opportunity in every workspace that uses the product, and recovery
// clears it. It reuses the impact enumerator to find affected workspaces and the
// same OpportunityStore the Action Center reads. Reads b.workspaceStore lazily
// via the builder.
type connectionHealthNotifier struct{ b *ServerBuilder }

func (n connectionHealthNotifier) OnGrantHealthChanged(ctx context.Context, product connections.ProductKey, credentialRef string, health connections.GrantHealth) {
	if n.b.workspaceStore == nil {
		return
	}
	workspaces, err := connectionImpactEnumerator{b: n.b}.WorkspacesUsingProduct(ctx, product, credentialRef)
	if err != nil {
		logger.Warn("connection health notify: impact lookup failed", logger.Fields{"error": err})
		return
	}
	opps := workspace.NewOpportunityStore(n.b.workspaceStore)
	title := healthOpportunityTitle(product)
	raise := isSurfacedUnhealthy(health)
	for _, ws := range workspaces {
		if raise {
			if _, _, err := opps.Upsert(workspace.Opportunity{
				WorkspaceID:       ws.ID,
				Title:             title, // stable → Upsert coalesces, never duplicates
				Summary:           healthOpportunitySummary(product, health),
				Priority:          "high",
				Confidence:        "high",
				RecommendedAction: "Reconnect your Google account in Settings → Google Account.",
			}); err != nil {
				logger.Warn("connection health notify: upsert failed", logger.Fields{"workspace": ws.ID, "error": err})
			}
		} else {
			n.clearOpportunity(opps, ws.ID, title)
		}
	}
}

func (n connectionHealthNotifier) clearOpportunity(opps workspace.OpportunityStore, workspaceID, title string) {
	list, err := opps.List(workspaceID)
	if err != nil {
		return
	}
	for _, o := range list {
		if o.Title == title {
			_ = opps.Delete(workspaceID, o.ID)
		}
	}
}

func healthOpportunityTitle(product connections.ProductKey) string {
	return fmt.Sprintf("Reconnect Google to restore %s", healthProductName(product))
}

func healthOpportunitySummary(product connections.ProductKey, health connections.GrantHealth) string {
	if health == connections.HealthRateLimited {
		return fmt.Sprintf("Google %s is rate limited and will retry automatically.", healthProductName(product))
	}
	return fmt.Sprintf("Google %s needs you to reconnect your account to keep working in this workspace.", healthProductName(product))
}

// isSurfacedUnhealthy reports whether a health state should raise a proactive
// finding. Healthy / not-enabled / transient states do not.
func isSurfacedUnhealthy(h connections.GrantHealth) bool {
	switch h {
	case connections.HealthReconnectRequired,
		connections.HealthProviderUnavailable,
		connections.HealthAdminBlocked,
		connections.HealthRateLimited,
		connections.HealthScopeUpgradeRequired:
		return true
	default:
		return false
	}
}

func healthProductName(p connections.ProductKey) string {
	switch p {
	case connections.ProductGmail:
		return "Gmail"
	case connections.ProductCalendar:
		return "Calendar"
	case connections.ProductDrive:
		return "Drive"
	default:
		return string(p)
	}
}
