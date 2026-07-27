package connectionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
)

type fakeImpacts struct {
	byProduct map[connections.ProductKey][]WorkspaceImpact
	gotRefs   map[connections.ProductKey]string
}

func (f *fakeImpacts) WorkspacesUsingProduct(_ context.Context, product connections.ProductKey, credentialRef string) ([]WorkspaceImpact, error) {
	if f.gotRefs == nil {
		f.gotRefs = map[connections.ProductKey]string{}
	}
	f.gotRefs[product] = credentialRef
	return f.byProduct[product], nil
}

func seededImpactHandler(t *testing.T, impacts ImpactEnumerator) *http.ServeMux {
	t.Helper()
	store := connections.NewStore(t.TempDir())
	conn := &connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "jane@example.com",
	}
	if err := conn.AttachMCPGrant(connections.ProductCalendar, "sub-1", "google-calendar", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(conn); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard(), Impacts: impacts})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestImpact_ListsWorkspacesForEnabledGrant(t *testing.T) {
	impacts := &fakeImpacts{byProduct: map[connections.ProductKey][]WorkspaceImpact{
		connections.ProductCalendar: {{ID: "ws1", Name: "Scheduling"}},
	}}
	mux := seededImpactHandler(t, impacts)

	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/impact", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp impactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var cal *productImpact
	for i := range resp.Products {
		if resp.Products[i].Product == connections.ProductCalendar {
			cal = &resp.Products[i]
		}
	}
	if cal == nil || len(cal.Workspaces) != 1 || cal.Workspaces[0].ID != "ws1" {
		t.Fatalf("expected Calendar impact ws1, got %+v", resp.Products)
	}
	// The enumerator is queried with the grant's SAFE server-name reference,
	// never a credential (FR 76).
	if got := impacts.gotRefs[connections.ProductCalendar]; got != "google-calendar" {
		t.Errorf("enumerator queried with ref %q, want the MCP server name", got)
	}
	// Products with no grant (Gmail, Drive) are absent from the preview.
	if len(resp.Products) != 1 {
		t.Errorf("only the enabled Calendar grant should appear, got %d products", len(resp.Products))
	}
}

func TestImpact_ProductFilter(t *testing.T) {
	impacts := &fakeImpacts{byProduct: map[connections.ProductKey][]WorkspaceImpact{
		connections.ProductCalendar: {{ID: "ws1", Name: "Scheduling"}},
	}}
	mux := seededImpactHandler(t, impacts)

	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/impact?product=drive", "")
	var resp impactResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Products) != 0 {
		t.Errorf("filtering to an unenabled product should yield no rows, got %+v", resp.Products)
	}
}

func TestImpact_NoIdentityEmpty(t *testing.T) {
	store := connections.NewStore(t.TempDir())
	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard(), Impacts: &fakeImpacts{}})
	mux := http.NewServeMux()
	h.Register(mux)

	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/impact", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp impactResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Products) != 0 {
		t.Errorf("no verified identity should yield an empty preview, got %+v", resp.Products)
	}
}
