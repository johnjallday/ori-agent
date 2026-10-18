package sessionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssistantLibraryCollectionCandidates_StrictOwnerQueryAndNoAutomaticPreparation(t *testing.T) {
	h, _, home, _ := assistantPortfolioHTTPFixture(t)
	h.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	call := func(method, query string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(method, "/library/arrangements/plan/collection-review/token/candidates"+query, home.ID, "")
		r.SetPathValue("arrangementID", "plan")
		r.SetPathValue("reviewToken", "token")
		w := httptest.NewRecorder()
		h.GetAssistantLibraryCollectionCandidates(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable candidate metadata")
		}
		return w
	}
	for _, query := range []string{"?query=a&query=b", "?owner=foreign", "?operation=0&operation=0", "?offset=-1", "?offset=%ZZ", "?query=a;other=1"} {
		if w := call(http.MethodGet, query); w.Code != http.StatusBadRequest {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	if w := call(http.MethodGet, "?query=song&operation=0&offset=0"); w.Code != http.StatusConflict {
		t.Fatal("missing service gained authority", w.Code)
	}
	if w := call(http.MethodPost, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
	if w := call(http.MethodGet, ""); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatal("owner boundary", w.Code, w.Body.String())
	}
}
