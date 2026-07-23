package connections

// This file defines the two state vocabularies the UI reads and the single
// function that derives the account-level state from the product grants. The
// account state is always *derived*, never stored, so there is exactly one
// source of truth (the grants plus the two transient flags) and the surface can
// never disagree with itself (FR 2).

// GrantHealth is a product grant's health. The full set is fixed by FR 41; the
// surface must render exactly one of these per product row.
type GrantHealth string

const (
	// HealthNotEnabled is the default: the user never enabled this product, so
	// there is no credential, scope, connection, or binding for it (FR 12).
	HealthNotEnabled GrantHealth = "not_enabled"
	// HealthConnecting marks an in-flight product authorization.
	HealthConnecting GrantHealth = "connecting"
	// HealthHealthy means the grant can refresh/connect and is usable.
	HealthHealthy GrantHealth = "healthy"
	// HealthScopeUpgradeRequired means Google granted a narrower scope set than
	// requested (e.g. the user deselected a permission), so a capability is
	// missing until re-authorized (FR 26).
	HealthScopeUpgradeRequired GrantHealth = "scope_upgrade_required"
	// HealthReconnectRequired means the grant is unhealthy (revoked/expired) and
	// needs a user-initiated reconnect; refresh must never open a browser on its
	// own (FR 75, 85).
	HealthReconnectRequired GrantHealth = "reconnect_required"
	// HealthAdvancedSetupRequired means no compatible operator-owned Web client is
	// stored yet, so first enablement must open the one-time Advanced step
	// (FR 27). Never applies to Gmail.
	HealthAdvancedSetupRequired GrantHealth = "advanced_setup_required"
	// HealthProviderUnavailable means the provider's (preview) service is
	// unavailable or the account is ineligible (FR 66, 74).
	HealthProviderUnavailable GrantHealth = "provider_unavailable"
	// HealthRateLimited means the provider is rate-limiting/quota-exhausting this
	// grant; Ori backs off rather than treating it as a token failure (FR 42).
	HealthRateLimited GrantHealth = "rate_limited"
	// HealthAdminBlocked means a Workspace administrator policy blocks the grant.
	HealthAdminBlocked GrantHealth = "admin_blocked"
	// HealthError is a catch-all failure that is not one of the specific states.
	HealthError GrantHealth = "error"
)

// IsEnabled reports whether the user has turned this product on at all. Only
// enabled grants influence the account-level state.
func (h GrantHealth) IsEnabled() bool {
	return h != HealthNotEnabled && h != ""
}

// IsHealthy reports whether the grant is currently usable.
func (h GrantHealth) IsHealthy() bool {
	return h == HealthHealthy
}

// NeedsAttention reports whether the grant is enabled but in a state that needs
// a user action to recover. HealthConnecting is deliberately excluded — it is
// in-flight progress, not a problem.
func (h GrantHealth) NeedsAttention() bool {
	switch h {
	case HealthScopeUpgradeRequired,
		HealthReconnectRequired,
		HealthAdvancedSetupRequired,
		HealthProviderUnavailable,
		HealthRateLimited,
		HealthAdminBlocked,
		HealthError:
		return true
	default:
		return false
	}
}

// ConnectionState is the account-level state shown at the top of the Google
// card. Exactly one is presented at a time (FR 2).
type ConnectionState string

const (
	// StateNotConnected: no verified identity yet.
	StateNotConnected ConnectionState = "not_connected"
	// StateConnecting: an identity handshake (or a sole product's first connect)
	// is in flight.
	StateConnecting ConnectionState = "connecting"
	// StatePartiallyConnected: identity is up and at least one product is healthy,
	// but another enabled product is still mid-connect — a transient rollout state.
	StatePartiallyConnected ConnectionState = "partially_connected"
	// StateConnected: identity is up and every product the user enabled is
	// healthy. A bare identity with no product enabled is Connected — the identity
	// itself is connected and product rows are independent (see the design note).
	StateConnected ConnectionState = "connected"
	// StateNeedsAttention: identity is up but at least one enabled product needs a
	// user action.
	StateNeedsAttention ConnectionState = "needs_attention"
	// StateDisconnecting: a whole-account disconnect is in flight.
	StateDisconnecting ConnectionState = "disconnecting"
)

// DeriveState computes the single account-level state from a connection's
// grants and transient flags. The precedence is deliberate:
//
//  1. Disconnecting wins over everything (a teardown is underway).
//  2. Without a verified subject there is no account: Connecting if a handshake
//     is in flight, else NotConnected.
//  3. With a subject, any enabled grant that needs attention surfaces as
//     NeedsAttention (a broken product must not hide behind a healthy one).
//  4. An identity-level re-handshake shows as Connecting.
//  5. A product mid-connect shows Partially connected if something else is
//     already healthy, else Connecting.
//  6. Otherwise the identity is up and all enabled grants (possibly none) are
//     healthy: Connected.
func DeriveState(c *Connection) ConnectionState {
	if c == nil {
		return StateNotConnected
	}
	if c.Disconnecting {
		return StateDisconnecting
	}
	if !c.HasVerifiedIdentity() {
		if c.Connecting {
			return StateConnecting
		}
		return StateNotConnected
	}

	var anyAttention, anyConnecting, anyHealthy bool
	for _, g := range c.Grants {
		if g == nil || !g.Health.IsEnabled() {
			continue
		}
		switch {
		case g.Health.NeedsAttention():
			anyAttention = true
		case g.Health == HealthConnecting:
			anyConnecting = true
		case g.Health.IsHealthy():
			anyHealthy = true
		}
	}

	switch {
	case anyAttention:
		return StateNeedsAttention
	case c.Connecting:
		return StateConnecting
	case anyConnecting && anyHealthy:
		return StatePartiallyConnected
	case anyConnecting:
		return StateConnecting
	default:
		return StateConnected
	}
}
