package connections

import (
	"errors"
	"fmt"
	"time"
)

// Grant-isolation invariants (FR 43, 44, 46). These mutation helpers touch one
// product at a time so a failure or teardown of one grant can never disturb
// another, and they refuse to rebind the identity to a different account.

// ErrSubjectMismatch is returned when a reconnect/repair comes back bound to a
// different Google account than the active identity.
var ErrSubjectMismatch = errors.New("connections: reconnect returned a different Google account")

// ErrAccountMismatch is returned when a legacy account being migrated belongs to
// a different email than the connected account, so it cannot be folded in (FR 89).
var ErrAccountMismatch = errors.New("connections: legacy account is a different Google account")

// SetGrantHealth updates only the named product's health, creating the grant
// record if absent. Other grants are left untouched (FR 43).
func (c *Connection) SetGrantHealth(p ProductKey, h GrantHealth) {
	if c == nil {
		return
	}
	if c.Grants == nil {
		c.Grants = map[ProductKey]*ProductGrant{}
	}
	g, ok := c.Grants[p]
	if !ok || g == nil {
		g = &ProductGrant{ConnectionID: c.ID, Product: p}
		c.Grants[p] = g
	}
	g.Health = h
	c.touch()
}

// DisableGrant removes only the named product's grant (the domain-level effect
// of a single-product disconnect). The identity and every other grant survive
// (FR 39, 79).
func (c *Connection) DisableGrant(p ProductKey) {
	if c == nil || c.Grants == nil {
		return
	}
	delete(c.Grants, p)
	c.touch()
}

// VerifyReconnectSubject enforces FR 46: a repair/reconnect that returns a
// different subject than the active identity must be rejected — the user is
// directed to Switch Account instead of silently rebinding to a new account.
func (c *Connection) VerifyReconnectSubject(returnedSubject string) error {
	if c == nil || returnedSubject == "" || returnedSubject != c.Subject {
		return ErrSubjectMismatch
	}
	return nil
}

func (c *Connection) touch() {
	c.UpdatedAt = time.Now()
}

// AttachMCPGrant attaches (or refreshes) a remote-MCP product grant — Calendar or
// Drive — after the MCP flow's ID token has been verified. It enforces that the
// verified subject matches the active identity (FR 23) and records the grant
// Healthy with an opaque reference to the MCP server; the tokens stay in the MCP
// vault seam, never on the connection (FR 40). It refuses a mismatched subject,
// a missing identity, or a non-MCP product.
func (c *Connection) AttachMCPGrant(product ProductKey, subject, credentialRef string, scopes []string) error {
	if c == nil || !c.HasVerifiedIdentity() {
		return ErrNoActiveIdentity
	}
	if subject == "" || subject != c.Subject {
		return ErrSubjectMismatch
	}
	if product != ProductCalendar && product != ProductDrive {
		return fmt.Errorf("connections: %q is not a remote-MCP product", product)
	}
	if c.Grants == nil {
		c.Grants = map[ProductKey]*ProductGrant{}
	}
	c.Grants[product] = &ProductGrant{
		ConnectionID:  c.ID,
		Product:       product,
		Transport:     TransportRemoteMCP,
		CredentialRef: credentialRef,
		GrantedScopes: scopes,
		Health:        HealthHealthy,
	}
	c.touch()
	return nil
}
