package sessionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssistantLibraryArrangementDecline_StrictOwnerAndRequest(t *testing.T) {
	h, _, home, _ := assistantPortfolioHTTPFixture(t)
	h.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	call := func(method, url, body string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(method, url, home.ID, body)
		r.SetPathValue("arrangementID", "proposal-a")
		w := httptest.NewRecorder()
		h.DeclineAssistantLibraryArrangement(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable decline result")
		}
		return w
	}
	valid := `{"expected_revision":1,"expected_record_revision":1}`
	for _, body := range []string{`{"expected_revision":1}`, `{"expected_revision":1,"expected_record_revision":null}`, `{"expected_revision":1,"expected_revision":2}`, `{"expected_revision":1,"expected_record_revision":1,"confirm":true}`, `{"expected_revision":1,"expected_record_revision":1,"owner":"foreign"}`} {
		if w := call(http.MethodPost, "/decline", body); w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call(http.MethodPost, "/decline?confirm=true", valid); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	if w := call(http.MethodPost, "/decline", valid); w.Code != http.StatusConflict {
		t.Fatal("missing service gained authority", w.Code)
	}
	if w := call(http.MethodGet, "/decline", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
	if w := call(http.MethodPost, "/decline", valid); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatal("foreign decline", w.Code)
	}
}
