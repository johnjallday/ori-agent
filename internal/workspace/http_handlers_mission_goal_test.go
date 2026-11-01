package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// FR9: the server refuses a manual goal run when the workspace has no goal,
// on both the Run now and the baseline endpoints, and never calls the runner.
func TestManualGoalRunRequiresAGoal(t *testing.T) {
	ts, store, trig := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "No Goal", Agents: []string{"Writer"}})
	if err := ws.SetEntryAgentName("Writer"); err != nil {
		t.Fatal(err)
	}
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

	// With a goal the same request starts a run in the background (202).
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = "Keep the brand voice consistent"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/mission/trigger", nil))
	ts.wg.Wait()
	if rec.Code != http.StatusAccepted || trig.callCount() != 1 {
		t.Fatalf("with a goal: status %d, calls %d: %s", rec.Code, trig.callCount(), rec.Body.String())
	}
}

func newGoalCharterMux(t *testing.T) (*http.ServeMux, Store, *Workspace) {
	t.Helper()
	ts, store, _ := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Newsletter", Agents: []string{"Writer"}})
	ws.Mission = "Already has a goal"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(store, nil, nil)
	handler.SetScheduler(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/mission", handler.GetMission)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/mission", handler.UpdateMission)
	return mux, store, ws
}

func patchGoal(mux *http.ServeMux, id, goal string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"mission": goal})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+id+"/mission", bytes.NewReader(body)))
	return rec
}

// FR3: the goal save bounds the goal at 4,000 runes and says so in plain words.
func TestUpdateMission_BoundsTheGoalAtFourThousandRunes(t *testing.T) {
	mux, store, ws := newGoalCharterMux(t)

	atCap := strings.Repeat("é", WorkspaceGoalMaxRunes)
	if rec := patchGoal(mux, ws.ID, atCap); rec.Code != http.StatusOK {
		t.Fatalf("a 4000-rune goal: status %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := store.Get(ws.ID); got.Mission != atCap {
		t.Fatalf("the 4000-rune goal was not saved whole")
	}

	rec := patchGoal(mux, ws.ID, atCap+"x")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Keep the goal within 4,000 characters.") {
		t.Fatalf("a 4001-rune goal: status %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := store.Get(ws.ID); got.Mission != atCap {
		t.Fatalf("a refused goal must not be saved")
	}
}

// FR5: untouched hint lines from the charter fill are dropped on save.
func TestUpdateMission_DropsUntouchedHintLines(t *testing.T) {
	mux, store, ws := newGoalCharterMux(t)
	goal := "## Outcome\nA monthly newsletter.\n## Maintain\n" + GoalHintLine(GoalCharterParts[1]) + "\n## Boundaries\nNever send anything."
	if rec := patchGoal(mux, ws.ID, goal); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := store.Get(ws.ID)
	if want := "## Outcome\nA monthly newsletter.\n## Maintain\n## Boundaries\nNever send anything."; got.Mission != want {
		t.Fatalf("saved goal = %q, want %q", got.Mission, want)
	}
}

// The fill-in refreshes a project's note from this event, so saving the same goal
// again, or changing something else, says nothing.
func TestUpdateMission_PublishesOnlyWhenTheGoalChanged(t *testing.T) {
	ts, store, _ := newSchedulerWithStub(t)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Charter", Agents: []string{"Writer"}})
	ws.Mission = "Already has a goal"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(8, 8)
	t.Cleanup(bus.Shutdown)
	seen := make(chan Event, 8)
	bus.SubscribeToEventType(EventGoalCharterChanged, func(event Event) { seen <- event })
	handler := NewHTTPHandler(store, nil, bus)
	handler.SetScheduler(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/mission", handler.UpdateMission)

	if rec := patchGoal(mux, ws.ID, "## Outcome\nA monthly newsletter."); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case event := <-seen:
		if event.WorkspaceID != ws.ID {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a changed goal published nothing")
	}
	if rec := patchGoal(mux, ws.ID, "## Outcome\nA monthly newsletter."); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case event := <-seen:
		t.Fatalf("the same goal published %+v", event)
	case <-time.After(200 * time.Millisecond):
	}
}

// FR4: the mission GET carries the Outcome line, the present parts and the
// five-part shape, so the editor never parses headings or duplicates copy.
func TestGetMission_CarriesTheGoalOutcomeAndCharterShape(t *testing.T) {
	mux, store, ws := newGoalCharterMux(t)
	get := func() map[string]json.RawMessage {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/mission", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	plain := get()
	if string(plain["goal_outcome"]) != `"Already has a goal"` || string(plain["goal_parts"]) != `[]` {
		t.Fatalf("plain goal: outcome %s parts %s", plain["goal_outcome"], plain["goal_parts"])
	}
	if string(plain["goal_digest"]) != `"`+GoalDigest("Already has a goal")+`"` {
		t.Fatalf("goal_digest = %s", plain["goal_digest"])
	}
	var shape []GoalCharterPart
	if err := json.Unmarshal(plain["goal_charter_shape"], &shape); err != nil || len(shape) != 5 {
		t.Fatalf("goal_charter_shape = %s (err %v)", plain["goal_charter_shape"], err)
	}
	for i, part := range GoalCharterParts {
		if shape[i] != part {
			t.Fatalf("shape[%d] = %+v, want %+v", i, shape[i], part)
		}
	}
	if !strings.Contains(string(plain["goal_charter_shape"]), `"hint":"What the agent keeps in good order on its own initiative."`) {
		t.Fatalf("the shape names each part's hint: %s", plain["goal_charter_shape"])
	}

	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = "## Boundaries\nNever send.\n## Outcome\nA monthly newsletter.\n## Build\nA template."
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	charter := get()
	if string(charter["goal_outcome"]) != `"A monthly newsletter."` || string(charter["goal_parts"]) != `["Outcome","Build","Boundaries"]` {
		t.Fatalf("charter: outcome %s parts %s", charter["goal_outcome"], charter["goal_parts"])
	}
}
