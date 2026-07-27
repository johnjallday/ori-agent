// Package connections holds Ori's provider-neutral account-connection domain:
// one external identity (e.g. a Google account) and the separate, explicitly
// enabled product grants (Gmail, Calendar, Drive) that hang off it.
//
// Design constraints this package deliberately encodes (see
// tasks/prd-google-account-connection.md):
//
//   - Identity is keyed on the provider's stable subject claim, never the email
//     address, which is mutable display metadata (FR 4).
//   - The domain holds only an *opaque credential reference* into the vault —
//     never an access/refresh token, authorization code, client secret, or raw
//     ID token. Credentials are write-only from the browser's perspective, so
//     nothing secret-bearing may ever reach a response (FR 32, 35). The
//     browser-facing projection lives in projection.go and excludes the
//     reference entirely.
//   - Product grants are independent: connecting the identity enables no
//     product, and one grant's failure must not disconnect the others
//     (FR 12, 39, 43).
//
// This file defines the core records and their invariants. It is intentionally
// inert: no HTTP, no persistence, no network — those layers wrap this domain in
// later groups. Keeping the model pure keeps the state machine (state.go) and
// projection (projection.go) trivially testable.
package connections

import "time"

// Provider identifies the external account provider. V1 ships only Google, but
// the domain is provider-neutral so a second provider never reshapes it.
type Provider string

// ProviderGoogle is the only provider in V1.
const ProviderGoogle Provider = "google"

// ProductKey names a product grant that can hang off a connection. Each is
// enabled independently and in context (FR 12).
type ProductKey string

const (
	ProductGmail    ProductKey = "gmail"
	ProductCalendar ProductKey = "calendar"
	ProductDrive    ProductKey = "drive"
)

// TransportType records how a product grant reaches its provider. Gmail uses
// Ori's native adapter; Calendar and Drive use remote MCP servers. The
// distinction matters because the two carry credentials through different vault
// seams and, critically, differ in whether the authorization flow can yield a
// verifiable subject for identity matching (see the spike doc).
type TransportType string

const (
	TransportNative    TransportType = "native"
	TransportRemoteMCP TransportType = "remote_mcp"
)

// Connection is one external identity plus its product grants. It is the
// internal record; the browser only ever sees the PublicConnection projection.
type Connection struct {
	// ID is Ori's internal, opaque connection identifier (not a secret, but not
	// meaningful outside Ori). It is stable across email changes.
	ID string `json:"id"`
	// Provider is the external account provider.
	Provider Provider `json:"provider"`
	// Subject is the provider's stable identity claim (Google OIDC `sub`). It is
	// the connection's primary identity key (FR 4). Empty until the identity
	// handshake has completed and validated an ID token.
	Subject string `json:"subject"`
	// Email is provider-supplied display metadata. It may change over the life of
	// the account and must never be used as the identity key (FR 4).
	Email string `json:"email"`
	// DisplayName is the provider-supplied display name, when available.
	DisplayName string `json:"display_name,omitempty"`
	// AvatarURL is the provider-supplied avatar, when available. It is proxied
	// server-side before display rather than hotlinked (see Technical notes).
	AvatarURL string `json:"avatar_url,omitempty"`
	// VaultID is the credential vault resolved for this connection (FR 9). Empty
	// until a vault has been chosen/created.
	VaultID string `json:"vault_id,omitempty"`
	// Grants holds the per-product grants, keyed by product. A product with no
	// entry (or a HealthNotEnabled entry) is simply not enabled.
	Grants map[ProductKey]*ProductGrant `json:"grants,omitempty"`
	// Connecting marks an in-flight identity handshake (transient). It does not
	// survive a completed connect and is never persisted as a durable state.
	Connecting bool `json:"connecting,omitempty"`
	// Disconnecting marks an in-flight whole-account disconnect (transient).
	Disconnecting bool `json:"disconnecting,omitempty"`

	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// ProductGrant is one product's authorization under a connection. It records
// only a reference to the vault-held credential — never the credential itself.
type ProductGrant struct {
	// ConnectionID links back to the parent connection (FR 37).
	ConnectionID string `json:"connection_id"`
	// Product is which product this grant authorizes.
	Product ProductKey `json:"product"`
	// Transport is how this grant reaches the provider.
	Transport TransportType `json:"transport"`
	// CredentialRef is an OPAQUE reference into the encrypted vault. It is a
	// lookup key, not a secret, and must never be returned to the browser.
	// Everything token-bearing stays behind this reference (FR 35).
	CredentialRef string `json:"credential_ref,omitempty"`
	// GrantedScopes is the EXACT scope set the provider reported granting — not
	// the requested set. Ori must never infer a scope was granted (FR 24).
	GrantedScopes []string `json:"granted_scopes,omitempty"`
	// TokenExpiry is the access-token expiry when known, for health checks.
	TokenExpiry *time.Time `json:"token_expiry,omitempty"`
	// Health is the grant's current health state (see state.go).
	Health GrantHealth `json:"health"`
}

// HasVerifiedIdentity reports whether the connection has a validated subject and
// can therefore own product grants keyed to that identity (FR 22–23).
func (c *Connection) HasVerifiedIdentity() bool {
	return c != nil && c.Subject != ""
}

// Grant returns the grant for a product and whether it exists.
func (c *Connection) Grant(p ProductKey) (*ProductGrant, bool) {
	if c == nil || c.Grants == nil {
		return nil, false
	}
	g, ok := c.Grants[p]
	return g, ok
}

// GrantHealthOf returns the health of a product's grant, treating a missing
// grant as HealthNotEnabled so callers never have to special-case absence.
func (c *Connection) GrantHealthOf(p ProductKey) GrantHealth {
	if g, ok := c.Grant(p); ok && g != nil {
		return g.Health
	}
	return HealthNotEnabled
}
