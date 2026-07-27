package connections

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func connWithGrants(products ...ProductKey) *Connection {
	c := &Connection{ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", Email: "j@example.com", Grants: map[ProductKey]*ProductGrant{}}
	for _, p := range products {
		c.Grants[p] = &ProductGrant{ConnectionID: "c1", Product: p, CredentialRef: "secret-ref-" + string(p), Health: HealthHealthy}
	}
	return c
}

func TestConsentLog_ReconcileGrantsThenWithdrawals(t *testing.T) {
	log := NewConsentLog(t.TempDir())

	// Enabling Gmail + Calendar records two grants.
	added, err := log.Reconcile(connWithGrants(ProductGmail, ProductCalendar))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 {
		t.Fatalf("expected 2 grant records, got %d", len(added))
	}

	// Reconciling the same state again is idempotent.
	added, _ = log.Reconcile(connWithGrants(ProductGmail, ProductCalendar))
	if len(added) != 0 {
		t.Errorf("re-reconcile should append nothing, got %d", len(added))
	}

	// Dropping Calendar records exactly one withdrawal.
	added, _ = log.Reconcile(connWithGrants(ProductGmail))
	if len(added) != 1 || added[0].Product != ProductCalendar || added[0].Action != ConsentWithdrawn {
		t.Fatalf("expected 1 Calendar withdrawal, got %+v", added)
	}

	// Whole-account disconnect (nil conn) withdraws the remaining Gmail consent.
	added, _ = log.Reconcile(nil)
	if len(added) != 1 || added[0].Product != ProductGmail || added[0].Action != ConsentWithdrawn {
		t.Fatalf("expected Gmail withdrawal on nil conn, got %+v", added)
	}

	// The full trail: 2 grants + 2 withdrawals, oldest-first.
	all, _ := log.List()
	if len(all) != 4 {
		t.Fatalf("expected 4 audit records, got %d", len(all))
	}
}

func TestConsentLog_RecordsAreSecretFree(t *testing.T) {
	dir := t.TempDir()
	log := NewConsentLog(dir)
	if _, err := log.Reconcile(connWithGrants(ProductGmail)); err != nil {
		t.Fatal(err)
	}
	// The on-disk audit file must never contain a credential reference.
	data, err := os.ReadFile(dir + "/connections/consent.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-ref") || strings.Contains(string(data), "credential") {
		t.Errorf("consent record leaked a credential reference: %s", data)
	}
	// Sanity: it is valid JSON with the expected safe fields.
	var recs []ConsentRecord
	if err := json.Unmarshal(data, &recs); err != nil || len(recs) != 1 {
		t.Fatalf("bad consent file: %v / %s", err, data)
	}
	if recs[0].Action != ConsentGranted || recs[0].DataPath != cloudModelDataPath {
		t.Errorf("unexpected record: %+v", recs[0])
	}
}
