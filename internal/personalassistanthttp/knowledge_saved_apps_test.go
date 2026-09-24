package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeAppSuggestions struct {
	userID string
	calls  int
	err    error
}

func (f *fakeAppSuggestions) Check(_ context.Context, userID string) ([]personalassistant.KnowledgeItem, error) {
	f.userID = userID
	f.calls++
	return nil, f.err
}

func TestSavedAppCheckRequiresExplicitEmptyPostAndServerUser(t *testing.T) {
	source := &fakeAppSuggestions{}
	h := NewHandler(nil, userprofile.LocalUserProvider{})
	h.SetSavedAppSuggestions(source)
	for _, scenario := range []struct {
		method, body string
		status       int
		calls        int
	}{
		{http.MethodGet, "", http.StatusMethodNotAllowed, 0},
		{http.MethodPost, `{"apps":["Obsidian"],"user_id":"foreign"}`, http.StatusBadRequest, 0},
		{http.MethodPost, "", http.StatusOK, 1},
	} {
		request := httptest.NewRequest(scenario.method, "/api/personal-assistant/knowledge/check-saved-apps", strings.NewReader(scenario.body))
		response := httptest.NewRecorder()
		h.CheckSavedAppSuggestions(response, request)
		if response.Code != scenario.status || source.calls != scenario.calls {
			t.Fatalf("%s %q status=%d calls=%d body=%q", scenario.method, scenario.body, response.Code, source.calls, response.Body.String())
		}
	}
	if source.userID != "local" {
		t.Fatalf("server did not resolve current user: %q", source.userID)
	}
}

func TestSavedAppCheckReportsPauseWithoutEchoingEvidence(t *testing.T) {
	source := &fakeAppSuggestions{err: personalassistant.ErrKnowledgePaused}
	h := NewHandler(nil, userprofile.LocalUserProvider{})
	h.SetSavedAppSuggestions(source)
	response := httptest.NewRecorder()
	h.CheckSavedAppSuggestions(response, httptest.NewRequest(http.MethodPost, "/check", nil))
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "Obsidian") {
		t.Fatalf("paused response=%d %q", response.Code, response.Body.String())
	}
	source.err = errors.New("private source failure /Users/test/secret")
	response = httptest.NewRecorder()
	h.CheckSavedAppSuggestions(response, httptest.NewRequest(http.MethodPost, "/check", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "/Users/test") {
		t.Fatalf("source error leaked: %d %q", response.Code, response.Body.String())
	}
}
