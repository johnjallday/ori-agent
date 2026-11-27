package sessionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomeMapFolders_OnlyThisOwnerAndHome(t *testing.T) {
	h, _, home, child := assistantPortfolioHTTPFixture(t)
	owner := home.OwnerUserID
	h.currentUserID = func(context.Context) (string, error) { return owner, nil }
	read := func(id string) *httptest.ResponseRecorder {
		resp := httptest.NewRecorder()
		h.GetHomeMapFolders(resp, assistantProgramRequest(http.MethodGet, "/map-folders", id, ""))
		return resp
	}
	resp := read(home.ID)
	if resp.Code != 200 || resp.Header().Get("Cache-Control") != "no-store" || !strings.Contains(resp.Body.String(), `"can_add_folder":false`) {
		t.Fatalf("read-only Home: %d %s", resp.Code, resp.Body)
	}
	if r := read(child.ID); r.Code != 404 {
		t.Fatal("ordinary member may not read Home folders")
	}
	owner = "another-owner"
	if r := read(home.ID); r.Code != 404 {
		t.Fatal("foreign owner may not read Home folders")
	}
}
