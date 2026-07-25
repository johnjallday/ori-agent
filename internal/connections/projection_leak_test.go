package connections

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProject_NeverLeaksSecrets is the FR 95 / Success-Metric-7 regression guard:
// the browser-safe projection must carry no credential reference, vault id, or
// any other secret-bearing value, no matter what the stored connection holds.
// If someone adds a token-bearing field to PublicConnection/PublicGrant, this
// test fails.
func TestProject_NeverLeaksSecrets(t *testing.T) {
	const (
		secretCredRef = "vault://email/SECRET-CRED-REF-do-not-leak"
		secretVault   = "VAULT-ID-SECRET-do-not-leak"
	)
	conn := &Connection{
		ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", Email: "jane@example.com",
		DisplayName: "Jane", VaultID: secretVault,
		Grants: map[ProductKey]*ProductGrant{
			ProductGmail: {
				ConnectionID: "c1", Product: ProductGmail,
				CredentialRef: secretCredRef, Health: HealthHealthy,
			},
			ProductCalendar: {
				ConnectionID: "c1", Product: ProductCalendar, Transport: TransportRemoteMCP,
				CredentialRef: "google-calendar", Health: HealthHealthy,
			},
		},
	}

	blob, err := json.Marshal(Project(conn))
	if err != nil {
		t.Fatal(err)
	}
	js := string(blob)

	// No secret value, and no JSON key that would carry one, may appear.
	for _, banned := range []string{
		secretCredRef, secretVault,
		"credential_ref", "credentialRef", "CredentialRef",
		"vault_id", "vaultID", "VaultID",
		"access_token", "refresh_token", "client_secret", "id_token",
	} {
		if strings.Contains(js, banned) {
			t.Errorf("projection leaked %q\nfull JSON: %s", banned, js)
		}
	}

	// Positive control: safe display fields ARE present, so we know the projection
	// isn't empty (which would pass the negative checks vacuously).
	for _, want := range []string{"jane@example.com", "\"subject\":\"sub-1\"", "\"product\":\"gmail\""} {
		if !strings.Contains(js, want) {
			t.Errorf("projection missing expected safe field %q", want)
		}
	}
}
