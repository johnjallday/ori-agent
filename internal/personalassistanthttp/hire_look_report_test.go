package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeHireLookActions struct {
	calls    []string
	startErr error
	retryErr error
	retried  personalassistant.HireRecord
}

func (f *fakeHireLookActions) StartSuggestion(_ context.Context, userID, hireID string) (personalassistant.FirstLookStarted, error) {
	f.calls = append(f.calls, "start "+userID+"/"+hireID)
	return personalassistant.FirstLookStarted{TaskID: "task-9", Route: "/workspaces/x/task/task-9"}, f.startErr
}

func (f *fakeHireLookActions) Dismiss(_ context.Context, userID, hireID string) error {
	f.calls = append(f.calls, "dismiss "+userID+"/"+hireID)
	return nil
}

func (f *fakeHireLookActions) Retry(_ context.Context, userID, hireID string) (personalassistant.HireRecord, error) {
	f.calls = append(f.calls, "retry "+userID+"/"+hireID)
	return f.retried, f.retryErr
}

func hireLookCall(t *testing.T, h *Handler, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hires/hire-1/"+action, strings.NewReader(body))
	r.SetPathValue("hireID", "hire-1")
	w := httptest.NewRecorder()
	switch action {
	case "dismiss":
		h.DismissHireLook(w, r)
	case "retry":
		h.RetryHireLook(w, r)
	default:
		h.StartHireLookSuggestion(w, r)
	}
	return w
}

// The hire's report names only the hire; the owner is the signed-in user and
// the body carries nothing.
func TestHireLookRoutes_AreOwnerScopedAndTakeNoRequestData(t *testing.T) {
	actions := &fakeHireLookActions{retried: personalassistant.HireRecord{ID: "hire-1", State: personalassistant.HireStateLooking}}
	h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	h.SetHireLookActions(actions)

	if w := hireLookCall(t, h, "start-suggestion", "{}"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"started":true`) {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	if w := hireLookCall(t, h, "dismiss", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dismissed":true`) {
		t.Fatalf("dismiss = %d %s", w.Code, w.Body.String())
	}
	if w := hireLookCall(t, h, "retry", "{}"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"retried":true`) {
		t.Fatalf("retry = %d %s", w.Code, w.Body.String())
	}
	if got := strings.Join(actions.calls, ","); got != "start owner-1/hire-1,dismiss owner-1/hire-1,retry owner-1/hire-1" {
		t.Fatalf("calls = %s", got)
	}
	for _, body := range []string{`{"workspace_id":"other"}`, "not json"} {
		if w := hireLookCall(t, h, "retry", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s => %d", body, w.Code)
		}
	}
	if len(actions.calls) != 3 {
		t.Fatalf("a refused body reached the service: %v", actions.calls)
	}
}

func TestHireLookRoutes_AnswerRefusalsWithCodes(t *testing.T) {
	h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	h.SetHireLookActions(&fakeHireLookActions{startErr: personalassistant.ErrFirstLookActedOn})
	if w := hireLookCall(t, h, "start-suggestion", "{}"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"already_acted"`) {
		t.Fatalf("acted on => %d %s", w.Code, w.Body.String())
	}
	h.SetHireLookActions(&fakeHireLookActions{retryErr: personalassistant.ErrHireLookNotFailed})
	if w := hireLookCall(t, h, "retry", "{}"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"not_failed"`) {
		t.Fatalf("not failed => %d %s", w.Code, w.Body.String())
	}
	h.SetHireLookActions(&fakeHireLookActions{retryErr: personalassistant.ErrFirstLookNotFound})
	if w := hireLookCall(t, h, "retry", "{}"); w.Code != http.StatusNotFound {
		t.Fatalf("not found => %d %s", w.Code, w.Body.String())
	}
	// A retry that could not start reports why, as the look's state.
	h.SetHireLookActions(&fakeHireLookActions{retried: personalassistant.HireRecord{
		ID: "hire-1", State: personalassistant.HireStateFailed, Failure: "a goal run is already in flight",
	}})
	if w := hireLookCall(t, h, "retry", "{}"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"retried":false`) ||
		!strings.Contains(w.Body.String(), "already in flight") {
		t.Fatalf("blocked retry => %d %s", w.Code, w.Body.String())
	}
	h.SetHireLookActions(&fakeHireLookActions{retryErr: errors.New("database is locked"), retried: personalassistant.HireRecord{ID: "hire-1"}})
	if w := hireLookCall(t, h, "retry", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("store failure => %d", w.Code)
	}
	unwired := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	if w := hireLookCall(t, unwired, "dismiss", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired => %d", w.Code)
	}
}
