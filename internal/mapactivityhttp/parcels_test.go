package mapactivityhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/mapactivity"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeCraft struct {
	amounts map[string]int64
}

func (f fakeCraft) RunCraft(_ context.Context, taskID, runKey string) (int64, bool, error) {
	amount, ok := f.amounts[taskID+"@"+runKey]
	return amount, ok, nil
}

type facts map[string]mapactivity.TaskFacts

func (f facts) TaskFacts(workspaceID, taskID string) (mapactivity.TaskFacts, bool) {
	value, ok := f[workspaceID+"/"+taskID]
	return value, ok
}

func newParcelMux(t *testing.T) (*http.ServeMux, *mapactivity.Tracker) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	started := time.Date(2026, 9, 16, 9, 58, 18, 0, time.UTC)
	tracker := mapactivity.NewTracker(nil)
	tracker.SetParcels(mapactivity.ParcelOptions{
		Store: mapactivity.NewSQLiteParcelStore(db),
		Tasks: facts{
			"ws-1/task-1": {Title: "Compare launch notes", Summary: "Two dates disagree.", StartedAt: &started},
			"ws-1/task-2": {Title: "Import the venue list", StartedAt: &started},
		},
	})
	handler := NewHandler(tracker)
	handler.SetCraftLookup(fakeCraft{amounts: map[string]int64{"task-1@run-1": 5}})
	mux := http.NewServeMux()
	handler.Register(mux)
	return mux, tracker
}

func finish(tracker *mapactivity.Tracker, eventType workspace.EventType, taskID string, data map[string]any) {
	ev := workspace.NewTaskEvent(eventType, "ws-1", taskID, "Theo", data)
	ev.Timestamp = time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	tracker.HandleEvent(ev)
}

func post(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return recorder
}

func TestOpenParcelReturnsTheResultCard(t *testing.T) {
	mux, tracker := newParcelMux(t)
	finish(tracker, workspace.EventTaskCompleted, "task-1", map[string]any{
		"run_id": "run-1", "result": "full result body",
		"xp_awarded": int64(50), "level_before": 1, "level_after": 2,
		"progress_before": 0.8, "progress_after": 0.3, "stage_before": "spark", "stage_after": "infant",
	})
	id := tracker.Snapshot().Parcels[0].ID

	recorder := post(mux, "/api/workspace-map/parcels/"+id+"/open", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var card CardPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.Parcel.ID != id || card.Parcel.Title != "Compare launch notes" || card.Parcel.RefID != "task-1" ||
		card.Parcel.OpenedAt == nil || card.Summary != "Two dates disagree." || card.FailureReason != "" {
		t.Fatalf("card = %+v", card)
	}
	if card.DurationSeconds == nil || *card.DurationSeconds != 102 {
		t.Fatalf("duration = %v, want 102 seconds", card.DurationSeconds)
	}
	if card.Rewards == nil || card.Rewards.XP == nil || card.Rewards.XP.Awarded != 50 ||
		card.Rewards.XP.StageAfter != "infant" || card.Rewards.Craft == nil || *card.Rewards.Craft != 5 {
		t.Fatalf("rewards = %+v", card.Rewards)
	}
	if strings.Contains(recorder.Body.String(), "full result body") {
		t.Fatal("the card carries the full result body, want the short summary only")
	}

	again := post(mux, "/api/workspace-map/parcels/"+id+"/open", "")
	if again.Code != http.StatusOK {
		t.Fatalf("second open status = %d, want 200", again.Code)
	}
	var second CardPayload
	_ = json.Unmarshal(again.Body.Bytes(), &second)
	if second.Parcel.OpenedAt == nil || !second.Parcel.OpenedAt.Equal(*card.Parcel.OpenedAt) {
		t.Fatalf("second open changed opened_at: %v vs %v", second.Parcel.OpenedAt, card.Parcel.OpenedAt)
	}
}

func TestOpenParcelOmitsRewardsItCannotShow(t *testing.T) {
	mux, tracker := newParcelMux(t)
	finish(tracker, workspace.EventTaskFailed, "task-2", map[string]any{"error": "the import file is missing"})
	id := tracker.Snapshot().Parcels[0].ID

	recorder := post(mux, "/api/workspace-map/parcels/"+id+"/open", "")
	var card CardPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.FailureReason != "the import file is missing" || card.Summary != "" || card.Rewards != nil {
		t.Fatalf("failed card = %+v, want the reason and no rewards block", card)
	}
	if strings.Contains(recorder.Body.String(), `"rewards"`) {
		t.Fatalf("empty rewards block serialized: %s", recorder.Body.String())
	}
}

func TestOpenParcelUnknownIDIs404(t *testing.T) {
	mux, _ := newParcelMux(t)
	if recorder := post(mux, "/api/workspace-map/parcels/nope/open", ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestOpenParcelsByRef(t *testing.T) {
	mux, tracker := newParcelMux(t)
	finish(tracker, workspace.EventTaskCompleted, "task-1", map[string]any{"run_id": "run-1"})

	recorder := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"task","workspace_id":"ws-1","ref_id":"task-1"}`)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"opened":1`) {
		t.Fatalf("open-by-ref = %d %s, want one opened", recorder.Code, recorder.Body.String())
	}
	if again := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"task","workspace_id":"ws-1","ref_id":"task-1"}`); !strings.Contains(again.Body.String(), `"opened":0`) {
		t.Fatalf("second open-by-ref = %s, want nothing left", again.Body.String())
	}
	if bad := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"chat","workspace_id":"ws-1","ref_id":"x"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d, want 400", bad.Code)
	}
	if bad := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"task","workspace_id":"ws-1"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("a task without its reference = %d, want 400", bad.Code)
	}
	// The console shows every waiting batch, so it opens them all.
	if all := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"file_janitor","workspace_id":"ws-1"}`); all.Code != http.StatusOK {
		t.Fatalf("janitor open without a reference = %d %s, want 200", all.Code, all.Body.String())
	}
}

func TestBriefAndJanitorCardsCarryTheirFixedSentence(t *testing.T) {
	mux, tracker := newParcelMux(t)
	scan := workspace.NewActivityEvent(workspace.EventActivityFinished, "ws-1", "file_janitor", "ws-1:1", map[string]any{
		"outcome": "succeeded", "count": 3, "ref_id": "batch-1",
	})
	tracker.HandleEvent(scan)
	parcels := tracker.Snapshot().Parcels
	if len(parcels) != 1 {
		t.Fatalf("parcels = %+v", parcels)
	}
	recorder := post(mux, "/api/workspace-map/parcels/"+parcels[0].ID+"/open", `{}`)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"summary":"3 files are ready to review."`) ||
		!strings.Contains(body, `"kind":"file_janitor"`) || strings.Contains(body, `"rewards"`) {
		t.Fatalf("card = %d %s", recorder.Code, body)
	}
}

func TestParcelRoutesAre404WhenTheFlagIsOff(t *testing.T) {
	mux, tracker := newParcelMux(t)
	finish(tracker, workspace.EventTaskCompleted, "task-1", map[string]any{"run_id": "run-1"})
	id := tracker.Snapshot().Parcels[0].ID

	t.Setenv("ORI_MAP_SHOW_ENABLED", "false")
	if recorder := post(mux, "/api/workspace-map/parcels/"+id+"/open", ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("open status = %d, want 404", recorder.Code)
	}
	if recorder := post(mux, "/api/workspace-map/parcels/open-by-ref", `{"kind":"task","workspace_id":"ws-1","ref_id":"task-1"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("open-by-ref status = %d, want 404", recorder.Code)
	}
}

func TestSnapshotListsParcelRowsOnly(t *testing.T) {
	mux, tracker := newParcelMux(t)
	finish(tracker, workspace.EventTaskCompleted, "task-1", map[string]any{"run_id": "run-1"})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspace-map/activity", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `"title":"Compare launch notes"`) {
		t.Fatalf("snapshot = %s, want the parcel row", body)
	}
	for _, banned := range []string{"Two dates disagree", "xp", "summary", "failure"} {
		if strings.Contains(body, banned) {
			t.Errorf("snapshot leaks %q: %s", banned, body)
		}
	}
}
