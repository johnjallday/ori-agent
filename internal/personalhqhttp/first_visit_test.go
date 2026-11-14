package personalhqhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func TestFirstVisitOnlyAcceptsDone(t *testing.T) {
	handler, _ := newTestHandler(t)
	for _, body := range []string{`{"state":"pending"}`, `{"state":""}`, `{}`, `null`, `{"state":"bogus"}`, `{"state":"done","user_id":"other"}`, `{"state":1}`, `{"state":"done"} {}`} {
		rec := httptest.NewRecorder()
		handler.SetFirstVisitState(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/first-visit-state", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	for range 2 {
		rec := httptest.NewRecorder()
		handler.SetFirstVisitState(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/first-visit-state", strings.NewReader(`{"state":"done"}`)))
		if rec.Code != http.StatusOK || decodeStatus(t, rec).FirstVisitState != userprofile.HQFirstVisitDone {
			t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	handler.Status(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/status", nil))
	if decodeStatus(t, rec).FirstVisitState != userprofile.HQFirstVisitDone {
		t.Fatal("status did not persist done")
	}
	if err := handler.service.OfferHQFirstVisit(t.Context(), "local"); err != nil {
		t.Fatal(err)
	}
	status, err := handler.service.Status(t.Context(), "local")
	if err != nil || status.FirstVisitState != userprofile.HQFirstVisitDone {
		t.Fatal("a done HQ was reoffered", status, err)
	}
	rec = httptest.NewRecorder()
	handler.SetFirstVisitState(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/first-visit-state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatal(rec.Code)
	}
}
