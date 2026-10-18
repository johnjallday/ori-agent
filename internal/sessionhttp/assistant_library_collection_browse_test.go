package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func TestAssistantLibraryCollectionBrowse_ReadOnlyOwnerAndStrictQuery(t *testing.T) {
	h, store, home, _ := assistantPortfolioHTTPFixture(t)
	h.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	state := home.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	call := func(method, query string, detail bool) *httptest.ResponseRecorder {
		r := assistantProgramRequest(method, "/library/collections"+query, home.ID, "")
		r.SetPathValue("collectionID", "unknown")
		w := httptest.NewRecorder()
		if detail {
			h.GetAssistantLibraryCollectionMembers(w, r)
		} else {
			h.GetAssistantLibraryCollections(w, r)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable owner metadata")
		}
		return w
	}
	if w := call(http.MethodGet, "", false); w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body.String())
	}
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "browse-init"); err != nil {
		t.Fatal(err)
	}
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	w := call(http.MethodGet, "", false)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var page projectlibrary.CollectionCatalogPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.ContextPin) != 64 || page.Total != 0 {
		t.Fatal(page, err)
	}
	for _, query := range []string{"?offset=0&offset=0", "?offset=abc", "?manager_name=forged", "?offset=%ZZ", "?context_pin=short", "?offset=0;other=1"} {
		if w := call(http.MethodGet, query, false); w.Code != http.StatusBadRequest {
			t.Fatal(query, w.Code)
		}
	}
	if w := call(http.MethodGet, "", true); w.Code != http.StatusBadRequest {
		t.Fatal("missing pin", w.Code)
	}
	if w := call(http.MethodGet, "?context_pin="+strings.Repeat("a", 64), true); w.Code != http.StatusConflict {
		t.Fatal("stale pin", w.Code)
	}
	if w := call(http.MethodPost, "", false); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
	if w := call(http.MethodGet, "", false); w.Code == http.StatusOK {
		t.Fatal("foreign owner")
	}
	after, err := library.Read(scope)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read initialized or migrated", err)
	}
}
