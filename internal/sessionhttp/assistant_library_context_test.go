package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func TestLibraryArrangementContext_RequiresOwnerNoQueriesAndSanitizesFailures(t *testing.T) {
	h, _, home, _ := assistantPortfolioHTTPFixture(t)
	h.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	calls := 0
	h.SetArrangementContext(func(_ context.Context, owner, homeID string) (projectlibrary.ArrangementPlanningContext, error) {
		calls++
		if owner != home.OwnerUserID || homeID != home.ID {
			t.Fatal("caller identity replaced owner")
		}
		return projectlibrary.ArrangementPlanningContext{}, errors.New("private-key /private/path")
	})
	call := func(query string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(http.MethodGet, "/library/arrangements/context"+query, home.ID, "")
		w := httptest.NewRecorder()
		h.GetAssistantLibraryArrangementContext(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable disclosure")
		}
		return w
	}
	if w := call("?manager=forged"); w.Code != http.StatusBadRequest || calls != 0 {
		t.Fatal("query reached resolver")
	}
	if w := call(""); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "private") || calls != 1 {
		t.Fatal("unsafe error", w.Body.String())
	}
	h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
	if w := call(""); w.Code != http.StatusNotFound || calls != 1 {
		t.Fatal("foreign owner resolved runtime")
	}
}
