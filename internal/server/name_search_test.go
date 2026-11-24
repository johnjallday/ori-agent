package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/namesearch"
)

// The by-name search is wired to the real session and workspace stores, answers
// through the real middleware chain, and answers a browser that is not on the
// Mac that runs Ori (it reads only Ori's own records).
func TestNameSearchRouteFindsAWorkspaceThroughTheRealStores(t *testing.T) {
	handler := newRoutesTestHandler(t)
	created := createRouteTestWorkspace(t, handler, "Harbor Album")

	post := func(target, body, peer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost"+target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = peer
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, peer := range []string{"127.0.0.1:43210", "192.0.2.5:1010"} {
		rec := post("/api/name-search", `{"term":"HARBOR"}`, peer)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", peer, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", peer, rec.Header().Get("Cache-Control"))
		}
		var out namesearch.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Kinds) != len(namesearch.Kinds) {
			t.Fatalf("%s: %d kinds", peer, len(out.Kinds))
		}
		for _, kind := range out.Kinds {
			// A fresh workspace has nothing in most kinds, but every real source
			// must be wired and readable: "unavailable" here means a missing seam.
			if kind.Status == namesearch.StatusUnavailable {
				t.Errorf("%s: %s is unavailable: %+v", peer, kind.Kind, kind)
			}
		}
		found := out.Kinds[0]
		if found.Kind != namesearch.KindWorkspaces || len(found.Items) != 1 ||
			found.Items[0].ID != created.ID || found.Items[0].RowKey != created.ID || found.Items[0].Label != "Harbor Album" {
			t.Fatalf("%s: workspaces = %+v", peer, found)
		}
	}

	// The term belongs in the body.
	if rec := post("/api/name-search?term=harbor", `{"term":"harbor"}`, "127.0.0.1:43210"); rec.Code != http.StatusBadRequest {
		t.Errorf("a term in the address: status %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/name-search", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", rec.Code)
	}
}
