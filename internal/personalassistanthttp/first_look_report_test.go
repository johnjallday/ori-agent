package personalassistanthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeFirstLookActions struct {
	starts, dismisses []string
	startErr          error
	started           personalassistant.FirstLookStarted
}

func (f *fakeFirstLookActions) StartSuggestion(_ context.Context, userID, lookTaskID string) (personalassistant.FirstLookStarted, error) {
	f.starts = append(f.starts, userID+"/"+lookTaskID)
	return f.started, f.startErr
}

func (f *fakeFirstLookActions) Dismiss(_ context.Context, userID, lookTaskID string) error {
	f.dismisses = append(f.dismisses, userID+"/"+lookTaskID)
	return nil
}

func firstLookCall(t *testing.T, h *Handler, action, body string) *httptest.ResponseRecorder {
	const taskID = "look-1"
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-look/"+taskID+"/"+action, strings.NewReader(body))
	r.SetPathValue("taskID", taskID)
	w := httptest.NewRecorder()
	if action == "dismiss" {
		h.DismissFirstLookSuggestion(w, r)
	} else {
		h.StartFirstLookSuggestion(w, r)
	}
	return w
}

// Start this and Not now name only the look; the owner is the signed-in user,
// and the body carries nothing.
func TestFirstLookRoutes_AreOwnerScopedAndTakeNoRequestData(t *testing.T) {
	actions := &fakeFirstLookActions{started: personalassistant.FirstLookStarted{TaskID: "task-9", Route: "/workspaces/x/task/task-9"}}
	h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	h.SetFirstLookActions(actions)

	if w := firstLookCall(t, h, "start-suggestion", "{}"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"started":true`) ||
		!strings.Contains(w.Body.String(), "task-9") {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	if w := firstLookCall(t, h, "dismiss", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dismissed":true`) {
		t.Fatalf("dismiss = %d %s", w.Code, w.Body.String())
	}
	if strings.Join(actions.starts, ",") != "owner-1/look-1" || strings.Join(actions.dismisses, ",") != "owner-1/look-1" {
		t.Fatalf("calls = %v %v", actions.starts, actions.dismisses)
	}
	for _, body := range []string{`{"suggestion":"rm -rf /"}`, `{"task_id":"other"}`, "not json"} {
		if w := firstLookCall(t, h, "start-suggestion", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s => %d", body, w.Code)
		}
	}
	if len(actions.starts) != 1 {
		t.Fatalf("a refused body reached the service: %v", actions.starts)
	}
	get := httptest.NewRequest(http.MethodGet, "/x", nil)
	get.SetPathValue("taskID", "look-1")
	gw := httptest.NewRecorder()
	h.StartFirstLookSuggestion(gw, get)
	if gw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET => %d", gw.Code)
	}
}

func TestFirstLookRoutes_AnswerRefusalsWithCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{personalassistant.ErrFirstLookNotFound, http.StatusNotFound, ""},
		{personalassistant.ErrFirstLookActedOn, http.StatusConflict, "already_acted"},
		{personalassistant.ErrFirstLookNoSuggestion, http.StatusConflict, "no_suggestion"},
		{personalassistant.ErrFirstLookNotFinished, http.StatusConflict, "not_finished"},
	} {
		h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
		h.SetFirstLookActions(&fakeFirstLookActions{startErr: tc.err})
		w := firstLookCall(t, h, "start-suggestion", "{}")
		if w.Code != tc.status || (tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`)) {
			t.Errorf("%v => %d %s", tc.err, w.Code, w.Body.String())
		}
	}
	unwired := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	if w := firstLookCall(t, unwired, "dismiss", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired => %d", w.Code)
	}
}
