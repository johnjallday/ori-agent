package connectionshttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
)

type fakeHealth struct {
	m map[connections.ProductKey]connections.GrantHealth
}

func (f fakeHealth) LiveHealth(product connections.ProductKey, _ string) (connections.GrantHealth, bool) {
	h, ok := f.m[product]
	return h, ok
}

func TestStatus_ReconcilesGrantHealth(t *testing.T) {
	store := connections.NewStore(t.TempDir())
	conn := &connections.Connection{ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "j@example.com"}
	if err := conn.AttachMCPGrant(connections.ProductCalendar, "sub-1", "google-calendar", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(conn); err != nil {
		t.Fatal(err)
	}

	// Live source reports the Calendar server has lost auth.
	health := fakeHealth{m: map[connections.ProductKey]connections.GrantHealth{
		connections.ProductCalendar: connections.HealthReconnectRequired,
	}}
	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard(), Health: health})
	mux := http.NewServeMux()
	h.Register(mux)

	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Grants []struct {
			Product string `json:"product"`
			Health  string `json:"health"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var calHealth string
	for _, g := range resp.Grants {
		if g.Product == "calendar" {
			calHealth = g.Health
		}
	}
	if calHealth != string(connections.HealthReconnectRequired) {
		t.Errorf("Calendar health = %q, want reconnect_required (reconciled without a browser)", calHealth)
	}

	// The reconciled health is persisted for the next load.
	reloaded, _ := store.Load()
	if g, ok := reloaded.Grant(connections.ProductCalendar); !ok || g.Health != connections.HealthReconnectRequired {
		t.Error("reconciled health should be persisted")
	}
}

func TestStatus_NoHealthCheckerKeepsStored(t *testing.T) {
	store := connections.NewStore(t.TempDir())
	conn := &connections.Connection{ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "j@example.com"}
	_ = conn.AttachMCPGrant(connections.ProductCalendar, "sub-1", "google-calendar", nil)
	_ = store.Save(conn)

	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard()}) // no Health checker
	mux := http.NewServeMux()
	h.Register(mux)

	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// Healthy stays healthy when no checker is wired.
	reloaded, _ := store.Load()
	if g, _ := reloaded.Grant(connections.ProductCalendar); g.Health != connections.HealthHealthy {
		t.Errorf("without a checker, stored health must be untouched, got %q", g.Health)
	}
}
