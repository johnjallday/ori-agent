package connections

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProject_AlwaysThreeRowsInOrder(t *testing.T) {
	pub := Project(conn("sub-1", grant(ProductGmail, HealthHealthy)))
	if len(pub.Grants) != 3 {
		t.Fatalf("want 3 product rows, got %d", len(pub.Grants))
	}
	want := []ProductKey{ProductGmail, ProductCalendar, ProductDrive}
	for i, g := range pub.Grants {
		if g.Product != want[i] {
			t.Fatalf("row %d = %q, want %q", i, g.Product, want[i])
		}
	}
	// Absent products default to Not enabled.
	if pub.Grants[1].Health != HealthNotEnabled || pub.Grants[1].Enabled {
		t.Fatalf("absent Calendar should be not-enabled, got %+v", pub.Grants[1])
	}
	if pub.Grants[0].Health != HealthHealthy || !pub.Grants[0].Enabled {
		t.Fatalf("Gmail should be healthy+enabled, got %+v", pub.Grants[0])
	}
}

func TestProject_Nil(t *testing.T) {
	pub := Project(nil)
	if pub.State != StateNotConnected {
		t.Fatalf("nil connection state = %q, want not_connected", pub.State)
	}
	if pub.Provider != ProviderGoogle || len(pub.Grants) != 3 {
		t.Fatalf("nil projection should still be a Google account with 3 rows: %+v", pub)
	}
}

// TestProject_NoSecretLeak is the load-bearing boundary test: a fully populated
// connection — including a credential reference and token expiry — must project
// to a JSON blob that carries none of the secret-bearing material (FR 35).
func TestProject_NoSecretLeak(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	c := &Connection{
		ID: "conn-9", Provider: ProviderGoogle, Subject: "sub-abc",
		Email: "jane@example.com", DisplayName: "Jane", AvatarURL: "https://ori.local/avatar/9",
		VaultID: "vault-1",
		Grants: map[ProductKey]*ProductGrant{
			ProductGmail: {
				ConnectionID: "conn-9", Product: ProductGmail, Transport: TransportNative,
				CredentialRef: "vault://gmail/supersecret-ref", GrantedScopes: []string{"openid", "gmail.readonly"},
				TokenExpiry: &exp, Health: HealthHealthy,
			},
		},
	}
	blob, err := json.Marshal(Project(c))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(blob)

	for _, forbidden := range []string{"credential", "supersecret-ref", "vault://", "vault-1", "refresh", "access_token", "client_secret"} {
		if strings.Contains(strings.ToLower(s), strings.ToLower(forbidden)) {
			t.Fatalf("projection leaked %q: %s", forbidden, s)
		}
	}
	// Safe fields must survive.
	for _, want := range []string{"sub-abc", "jane@example.com", "gmail.readonly", "healthy"} {
		if !strings.Contains(s, want) {
			t.Fatalf("projection dropped safe field %q: %s", want, s)
		}
	}
}
