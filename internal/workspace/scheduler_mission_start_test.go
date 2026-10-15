package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// blockingStarter is a MissionTrigger + MissionRunStarter whose execution
// waits on release, so a test can observe a run while it is in flight.
type blockingStarter struct {
	mu       sync.Mutex
	starts   []MissionRunOptions
	release  chan struct{}
	executed chan string
}

func newBlockingStarter() *blockingStarter {
	return &blockingStarter{release: make(chan struct{}), executed: make(chan string, 8)}
}

func (s *blockingStarter) TriggerMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int) (string, error) {
	return "", errors.New("the scheduler must use StartMissionRun")
}

func (s *blockingStarter) StartMissionRun(_ context.Context, workspaceID string, _ int, opts MissionRunOptions) (string, func() error, error) {
	s.mu.Lock()
	s.starts = append(s.starts, opts)
	runID := "run-" + string(rune('a'+len(s.starts)-1))
	s.mu.Unlock()
	return runID, func() error {
		<-s.release
		s.executed <- runID
		return nil
	}, nil
}

func (s *blockingStarter) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.starts)
}

func goalStartFixture(t *testing.T) (*TaskScheduler, *InMemoryStore, *blockingStarter, *HTTPHandler, *http.ServeMux, *Workspace) {
	t.Helper()
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand", Agents: []string{"Writer"}})
	ws.Mission = "Keep the brand voice consistent"
	if err := ws.SetEntryAgentName("Writer"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	ts := NewTaskScheduler(store, SchedulerConfig{PollInterval: time.Minute})
	starter := newBlockingStarter()
	ts.SetMissionTrigger(starter)
	t.Cleanup(func() {
		select {
		case <-starter.release:
		default:
			close(starter.release)
		}
		ts.wg.Wait()
	})
	handler := NewHTTPHandler(store, nil, nil)
	handler.SetScheduler(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/mission", handler.GetMission)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/mission", handler.UpdateMission)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/trigger", handler.TriggerMission)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/baseline", handler.RunBaselineNow)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/first-open", handler.GoalFirstOpen)
	return ts, store, starter, handler, mux, ws
}

func serveJSON(t *testing.T, mux *http.ServeMux, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	mux.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

// FR26/FR27: Run now answers 202 with a run id while the run executes in the
// background, GET mission shows it in flight, a second trigger is 409, and
// the slot frees when the run ends.
func TestRunNowIsAsynchronousAndSingleFlight(t *testing.T) {
	ts, _, starter, _, mux, ws := goalStartFixture(t)
	base := "/api/workspaces/" + ws.ID + "/mission"

	code, body := serveJSON(t, mux, http.MethodPost, base+"/trigger", "")
	if code != http.StatusAccepted || body["run_id"] != "run-a" || body["status"] != "running" {
		t.Fatalf("trigger = %d %v", code, body)
	}
	if got := starter.starts[0].Origin; got != MissionOriginManual {
		t.Fatalf("origin = %q, want manual", got)
	}
	_, state := serveJSON(t, mux, http.MethodGet, base, "")
	detail, _ := state["run_in_flight_detail"].(map[string]any)
	if state["run_in_flight"] != true || detail["run_id"] != "run-a" || detail["origin"] != MissionOriginManual {
		t.Fatalf("in-flight state = %v", state)
	}

	for _, endpoint := range []string{"/trigger", "/baseline"} {
		code, body = serveJSON(t, mux, http.MethodPost, base+endpoint, "")
		if code != http.StatusConflict || body["code"] != "goal_run_in_progress" {
			t.Fatalf("%s while in flight = %d %v", endpoint, code, body)
		}
	}
	if starter.startCount() != 1 {
		t.Fatalf("a second run started: %d", starter.startCount())
	}

	close(starter.release)
	if got := <-starter.executed; got != "run-a" {
		t.Fatalf("executed %q", got)
	}
	ts.wg.Wait()
	if _, running := ts.MissionRunning(ws.ID); running {
		t.Fatal("the in-flight slot must free when the run ends")
	}
	starter.release = make(chan struct{})
	if code, body = serveJSON(t, mux, http.MethodPost, base+"/baseline", ""); code != http.StatusAccepted {
		t.Fatalf("a new run after the first ended = %d %v", code, body)
	}
}

// The cadence shares the slot: a manual run is refused while a cadence run
// holds it.
func TestStartMissionRunSharesTheCadenceSlot(t *testing.T) {
	ts, _, _, _, _, ws := goalStartFixture(t)
	if !ts.claimMission(ws.ID) {
		t.Fatal("claim")
	}
	defer ts.releaseMission(ws.ID)
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); !errors.Is(err, ErrMissionRunInFlight) {
		t.Fatalf("err = %v, want ErrMissionRunInFlight", err)
	}
	if running, ok := ts.MissionRunning(ws.ID); !ok || running.Origin != MissionOriginCadence {
		t.Fatalf("running = %+v %v", running, ok)
	}
}

// FR24: with no model the run does not start; the reason is stored on the goal
// and shown by GET mission, and a later start that succeeds clears it.
func TestStartMissionRunRecordsWhyItCannotStart(t *testing.T) {
	ts, store, starter, _, mux, ws := goalStartFixture(t)
	modelReady := false
	ts.SetMissionModelCheck(func(*Workspace) (bool, string) { return modelReady, "" })

	code, body := serveJSON(t, mux, http.MethodPost, "/api/workspaces/"+ws.ID+"/mission/trigger", "")
	if code != http.StatusPreconditionFailed || body["code"] != "goal_no_model" || body["message"] != MissionNoModelReason {
		t.Fatalf("no-model trigger = %d %v", code, body)
	}
	if starter.startCount() != 0 {
		t.Fatal("a run started without a model")
	}
	_, state := serveJSON(t, mux, http.MethodGet, "/api/workspaces/"+ws.ID+"/mission", "")
	if state["needs_attention"] != MissionNoModelReason {
		t.Fatalf("needs_attention = %v", state["needs_attention"])
	}

	modelReady = true
	if _, err := ts.StartMissionRun(ws.ID, MissionOriginManual); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := store.Get(ws.ID); fresh.GoalToolboxPolicy != nil && fresh.GoalToolboxPolicy.NeedsAttention {
		t.Fatal("a run that starts must clear the stored reason")
	}

	// A workspace with no agent says so.
	lonely := NewWorkspace(CreateWorkspaceParams{Name: "Lonely"})
	lonely.Mission = "Ship it"
	if err := store.Save(lonely); err != nil {
		t.Fatal(err)
	}
	var blocked *MissionBlockedError
	if _, err := ts.StartMissionRun(lonely.ID, MissionOriginManual); !errors.As(err, &blocked) || blocked.Code != "goal_no_agent" {
		t.Fatalf("no-agent err = %v", err)
	}
}

// FR24: saving a goal where there was none starts exactly one run, ignoring
// MissionEnabled and leaving the cadence alone; editing an existing goal does not.
func TestFirstGoalSaveStartsOneRun(t *testing.T) {
	_, store, starter, _, mux, ws := goalStartFixture(t)
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/workspaces/" + ws.ID + "/mission"

	code, body := serveJSON(t, mux, http.MethodPut, path, `{"mission":"Grow the newsletter"}`)
	firstRun, _ := body["first_run"].(map[string]any)
	if code != http.StatusOK || firstRun["started"] != true || firstRun["run_id"] != "run-a" {
		t.Fatalf("first goal save = %d %v", code, body)
	}
	if starter.startCount() != 1 || starter.starts[0].Origin != MissionOriginFirstGoal {
		t.Fatalf("starts = %+v", starter.starts)
	}
	fresh, _ := store.Get(ws.ID)
	if fresh.MissionEnabled || fresh.Cadence != nil || fresh.NextMissionRunAt != nil {
		t.Fatalf("the first run must not enable or schedule the goal: %+v", fresh)
	}

	_, body = serveJSON(t, mux, http.MethodPut, path, `{"mission":"Grow the newsletter to 1,000 readers"}`)
	if _, present := body["first_run"]; present || starter.startCount() != 1 {
		t.Fatalf("editing a goal must not start a run: %v (starts %d)", body, starter.startCount())
	}
}

// FR25: a workspace created with a goal runs once, on first open; the marker
// is consumed so reloads start nothing; a workspace without it starts nothing.
func TestGoalFirstOpenStartsTheDeferredRunOnce(t *testing.T) {
	_, store, starter, _, mux, ws := goalStartFixture(t)
	path := "/api/workspaces/" + ws.ID + "/mission/first-open"

	code, body := serveJSON(t, mux, http.MethodPost, path, "")
	if code != http.StatusOK || body["pending"] != false || starter.startCount() != 0 {
		t.Fatalf("no marker = %d %v (starts %d)", code, body, starter.startCount())
	}

	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.GoalFirstRunPending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, body = serveJSON(t, mux, http.MethodPost, path, "")
	firstRun, _ := body["first_run"].(map[string]any)
	if code != http.StatusOK || body["pending"] != true || firstRun["started"] != true || starter.startCount() != 1 {
		t.Fatalf("first open = %d %v (starts %d)", code, body, starter.startCount())
	}
	if fresh, _ := store.Get(ws.ID); fresh.GoalFirstRunPending {
		t.Fatal("the marker must be consumed")
	}
	if _, body = serveJSON(t, mux, http.MethodPost, path, ""); body["pending"] != false || starter.startCount() != 1 {
		t.Fatalf("a second open started another run: %v", body)
	}
}
