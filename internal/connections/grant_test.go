package connections

import (
	"errors"
	"testing"
)

func TestSetGrantHealth_Isolated(t *testing.T) {
	c := conn("sub-1", grant(ProductGmail, HealthHealthy))
	c.SetGrantHealth(ProductCalendar, HealthReconnectRequired)

	if c.GrantHealthOf(ProductGmail) != HealthHealthy {
		t.Fatal("setting Calendar must not disturb Gmail (FR 43)")
	}
	if c.GrantHealthOf(ProductCalendar) != HealthReconnectRequired {
		t.Fatal("Calendar health not applied")
	}
}

func TestDisableGrant_Isolated(t *testing.T) {
	c := conn("sub-1", grant(ProductGmail, HealthHealthy), grant(ProductDrive, HealthHealthy))
	c.DisableGrant(ProductDrive)

	if _, ok := c.Grant(ProductDrive); ok {
		t.Fatal("disabled Drive grant should be gone")
	}
	if c.GrantHealthOf(ProductGmail) != HealthHealthy {
		t.Fatal("Gmail grant must survive Drive disable")
	}
	if !c.HasVerifiedIdentity() {
		t.Fatal("identity must survive a product disconnect")
	}
}

func TestVerifyReconnectSubject(t *testing.T) {
	c := conn("sub-1")
	if err := c.VerifyReconnectSubject("sub-1"); err != nil {
		t.Fatalf("matching subject should pass: %v", err)
	}
	if err := c.VerifyReconnectSubject("sub-2"); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("different subject must be rejected, got %v", err)
	}
	if err := c.VerifyReconnectSubject(""); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("empty subject must be rejected, got %v", err)
	}
}

func TestAttachMCPGrant(t *testing.T) {
	c := conn("sub-1")

	if err := c.AttachMCPGrant(ProductCalendar, "sub-1", "google-calendar", []string{"cal.readonly"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	g, ok := c.Grant(ProductCalendar)
	if !ok || g.Health != HealthHealthy || g.Transport != TransportRemoteMCP || g.CredentialRef != "google-calendar" {
		t.Fatalf("calendar grant = %+v", g)
	}

	if err := c.AttachMCPGrant(ProductCalendar, "sub-OTHER", "x", nil); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("mismatched subject: want ErrSubjectMismatch, got %v", err)
	}
	if err := c.AttachMCPGrant(ProductGmail, "sub-1", "x", nil); err == nil {
		t.Fatal("Gmail is native, not a remote-MCP product")
	}
	empty := &Connection{Provider: ProviderGoogle}
	if err := empty.AttachMCPGrant(ProductCalendar, "sub-1", "x", nil); !errors.Is(err, ErrNoActiveIdentity) {
		t.Fatalf("no identity: want ErrNoActiveIdentity, got %v", err)
	}
}
