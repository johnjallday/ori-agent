package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// FR9: the server refuses a manual goal run when the workspace has no goal,
// on both the Run now and the baseline endpoints, and never calls the runner.
func TestManualGoalRunRequiresAGoal(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "No Goal"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.TriggerMissionManually(context.Background(), ws.ID); !errors.Is(err, ErrMissionGoalRequired) {
		t.Fatalf("TriggerMissionManually err = %v, want ErrMissionGoalRequired", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	handler.SetScheduler(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/trigger", handler.TriggerMission)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/baseline", handler.RunBaselineNow)

	for _, endpoint := range []string{"trigger", "baseline"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/mission/"+endpoint, nil))
		if rec.Code != http.StatusPreconditionFailed {
			t.Fatalf("%s status = %d, want 412: %s", endpoint, rec.Code, rec.Body.String())
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "goal_required" {
			t.Fatalf("%s body = %s (err %v), want code goal_required", endpoint, rec.Body.String(), err)
		}
	}
	if trig.callCount() != 0 {
		t.Fatalf("a run started without a goal: %d calls", trig.callCount())
	}

	// With a goal the same request runs.
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = "Keep the brand voice consistent"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/mission/trigger", nil))
	if rec.Code != http.StatusOK || trig.callCount() != 1 {
		t.Fatalf("with a goal: status %d, calls %d: %s", rec.Code, trig.callCount(), rec.Body.String())
	}
}
