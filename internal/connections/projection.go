package connections

import "time"

// This file defines the ONLY connection shape the browser is allowed to see.
// Handlers must return a PublicConnection, never a Connection or ProductGrant,
// because the internal records carry a credential reference and the projection
// does not. The projection is built by explicit field copy (not struct
// embedding) so a future field added to the internal record can never silently
// leak into a response — a reviewer sees exactly what crosses the boundary
// (FR 33, 35). projection_test.go asserts the serialized form carries no
// token/secret/credential field.

// productOrder fixes the row order so the surface always shows Gmail, Calendar,
// then Drive (FR 11), regardless of map iteration order or which products the
// user has enabled.
var productOrder = AllProducts()

// AllProducts returns the V1 products in stable display order (Gmail, Calendar,
// Drive) — the canonical order used by the card and the disconnect impact
// preview (FR 11). It returns a fresh slice so callers cannot mutate the order.
func AllProducts() []ProductKey {
	return []ProductKey{ProductGmail, ProductCalendar, ProductDrive}
}

// PublicGrant is the safe, per-product summary shown in a product row. It
// deliberately omits CredentialRef and anything token-bearing.
type PublicGrant struct {
	Product       ProductKey    `json:"product"`
	Health        GrantHealth   `json:"health"`
	Enabled       bool          `json:"enabled"`
	Transport     TransportType `json:"transport,omitempty"`
	GrantedScopes []string      `json:"granted_scopes,omitempty"`
	ExpiresAt     *time.Time    `json:"expires_at,omitempty"`
}

// PublicConnection is the safe projection of a connection for UI consumption. It
// carries only display metadata and product-grant summaries (FR 33). The
// account State is always derived here, never trusted from storage.
type PublicConnection struct {
	ID          string          `json:"id"`
	Provider    Provider        `json:"provider"`
	Subject     string          `json:"subject"`
	Email       string          `json:"email"`
	DisplayName string          `json:"display_name,omitempty"`
	AvatarURL   string          `json:"avatar_url,omitempty"`
	State       ConnectionState `json:"state"`
	Grants      []PublicGrant   `json:"grants"`
}

// Project builds the browser-safe projection of a connection. It always emits
// the three product rows in a stable order (defaulting a missing product to
// Not enabled) and derives the account state from the live grants. A nil
// connection projects as a Not-connected Google account with three Not-enabled
// rows, so callers never special-case "no connection yet".
func Project(c *Connection) PublicConnection {
	pub := PublicConnection{
		Provider: ProviderGoogle,
		State:    DeriveState(c),
		Grants:   make([]PublicGrant, 0, len(productOrder)),
	}
	if c != nil {
		pub.ID = c.ID
		if c.Provider != "" {
			pub.Provider = c.Provider
		}
		pub.Subject = c.Subject
		pub.Email = c.Email
		pub.DisplayName = c.DisplayName
		pub.AvatarURL = c.AvatarURL
	}

	for _, product := range productOrder {
		pub.Grants = append(pub.Grants, projectGrant(c, product))
	}
	return pub
}

// projectGrant summarizes one product, treating a missing grant as Not enabled.
// It copies only safe fields — CredentialRef is never read here.
func projectGrant(c *Connection, product ProductKey) PublicGrant {
	g, ok := c.Grant(product)
	if !ok || g == nil {
		return PublicGrant{Product: product, Health: HealthNotEnabled, Enabled: false}
	}
	pg := PublicGrant{
		Product:       product,
		Health:        g.Health,
		Enabled:       g.Health.IsEnabled(),
		Transport:     g.Transport,
		GrantedScopes: g.GrantedScopes,
		ExpiresAt:     g.TokenExpiry,
	}
	if pg.Health == "" {
		pg.Health = HealthNotEnabled
	}
	return pg
}
