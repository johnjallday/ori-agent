package connectionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
)

type fakeTeardown struct {
	disconnected map[connections.ProductKey]string // product -> credentialRef seen
	unlinked     []string                          // "product|workspace"
}

func (f *fakeTeardown) DisconnectProduct(_ context.Context, product connections.ProductKey, credentialRef string) error {
	if f.disconnected == nil {
		f.disconnected = map[connections.ProductKey]string{}
	}
	f.disconnected[product] = credentialRef
	return nil
}

func (f *fakeTeardown) UnlinkProductFromWorkspace(_ context.Context, product connections.ProductKey, _, workspaceID string) error {
	f.unlinked = append(f.unlinked, string(product)+"|"+workspaceID)
	return nil
}

func seedTwoGrantConn(t *testing.T) *connections.Store {
	t.Helper()
	store := connections.NewStore(t.TempDir())
	conn := &connections.Connection{ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "jane@example.com"}
	if err := conn.AttachMCPGrant(connections.ProductCalendar, "sub-1", "google-calendar", nil); err != nil {
		t.Fatal(err)
	}
	conn.Grants[connections.ProductGmail] = &connections.ProductGrant{
		ConnectionID: "c1", Product: connections.ProductGmail, CredentialRef: "vault://email/acct-1", Health: connections.HealthHealthy,
	}
	if err := store.Save(conn); err != nil {
		t.Fatal(err)
	}
	return store
}

func teardownHandler(store *connections.Store, td ProductTeardown) *http.ServeMux {
	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard(), Teardown: td})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestProductDisconnect_IsolatesOtherGrants(t *testing.T) {
	store := seedTwoGrantConn(t)
	td := &fakeTeardown{}
	mux := teardownHandler(store, td)

	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/product/disconnect?product=calendar", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// Teardown was called with the grant's safe server-name reference.
	if td.disconnected[connections.ProductCalendar] != "google-calendar" {
		t.Errorf("teardown ref = %q, want google-calendar", td.disconnected[connections.ProductCalendar])
	}
	// Calendar grant dropped; Gmail grant intact (FR 79 isolation).
	conn, _ := store.Load()
	if _, ok := conn.Grant(connections.ProductCalendar); ok {
		t.Error("Calendar grant should be removed")
	}
	if _, ok := conn.Grant(connections.ProductGmail); !ok {
		t.Error("Gmail grant must survive a Calendar disconnect")
	}
	if conn.Subject != "sub-1" {
		t.Error("identity must survive a single-product disconnect")
	}
}

func TestWholeAccountDisconnect_TearsDownEveryProduct(t *testing.T) {
	store := seedTwoGrantConn(t)
	td := &fakeTeardown{}
	mux := teardownHandler(store, td)

	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/disconnect", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if _, ok := td.disconnected[connections.ProductCalendar]; !ok {
		t.Error("whole-account disconnect must tear down Calendar")
	}
	if _, ok := td.disconnected[connections.ProductGmail]; !ok {
		t.Error("whole-account disconnect must tear down Gmail")
	}
	// The connection record is gone (recoverable — bindings survive elsewhere).
	if conn, _ := store.Load(); conn != nil {
		t.Error("whole-account disconnect must delete the connection record")
	}
}

func TestProductUnlink_KeepsGlobalGrant(t *testing.T) {
	store := seedTwoGrantConn(t)
	td := &fakeTeardown{}
	mux := teardownHandler(store, td)

	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/product/unlink?product=calendar&workspace_id=ws1", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(td.unlinked) != 1 || td.unlinked[0] != "calendar|ws1" {
		t.Fatalf("unlink not invoked correctly: %v", td.unlinked)
	}
	// The global grant is untouched by an unlink (FR 78).
	conn, _ := store.Load()
	if _, ok := conn.Grant(connections.ProductCalendar); !ok {
		t.Error("unlink must NOT drop the global grant")
	}
}

func TestProductDisconnect_UnknownProduct(t *testing.T) {
	store := seedTwoGrantConn(t)
	mux := teardownHandler(store, &fakeTeardown{})
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/product/disconnect?product=bogus", "http://localhost")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown product should be 400, got %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "unknown_product" {
		t.Errorf("error = %q, want unknown_product", body["error"])
	}
}
