package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type bulkRunnerStub struct {
	receipt reaper.BulkReceipt
	err     error
	calls   int
	plan    reaper.BulkPlan
}

func (r *bulkRunnerStub) RunBulkPlan(_ context.Context, plan reaper.BulkPlan) (reaper.BulkReceipt, error) {
	r.calls++
	r.plan = plan
	return r.receipt, r.err
}

func planHandler(t *testing.T, runner BulkEditRunner) (*Handler, *http.ServeMux) {
	t.Helper()
	return planHandlerWithState(t, runner, reaper.State{
		Connected:            true,
		PlayState:            "stopped",
		Tracks:               []reaper.Track{{Index: 1, Name: "Drums"}, {Index: 2, Name: "Bass"}},
		TrackCount:           2,
		FolderDepthAvailable: true,
	})
}

func planHandlerWithState(t *testing.T, runner BulkEditRunner, state reaper.State) (*Handler, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{state: state}, nil)
	if runner != nil {
		handler.SetBulkEditRunner(runner)
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	return handler, mux
}

func TestProposeTrackEditsRefusedWithoutTheGrant(t *testing.T) {
	handler, _ := planHandler(t, nil)
	tools := handler.AgentTools("mine", "agent-1")
	var proposeTool interface {
		Call(context.Context, string) (string, error)
	}
	for _, tool := range tools {
		if tool.Definition().Name == "propose_reaper_track_edits" {
			proposeTool = tool
		}
	}
	if proposeTool == nil {
		t.Fatal("propose_reaper_track_edits tool not registered")
	}
	_, err := proposeTool.Call(context.Background(), `{"edits":[{"index":1,"expected_name":"Drums","operation":"mute","new_value":true}]}`)
	if !errors.Is(err, ErrAgentRuntimeGrantRequired) {
		t.Fatalf("propose without grant = %v", err)
	}
}

func TestProposingAPlanAppliesNothing(t *testing.T) {
	runner := &bulkRunnerStub{}
	handler, mux := planHandler(t, runner)
	ws, err := handler.store.Get("mine")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	tools := handler.AgentTools("mine", "agent-1")
	var proposeTool interface {
		Call(context.Context, string) (string, error)
	}
	for _, tool := range tools {
		if tool.Definition().Name == "propose_reaper_track_edits" {
			proposeTool = tool
		}
	}
	result, err := proposeTool.Call(context.Background(),
		`{"edits":[{"index":1,"expected_name":"Drums","operation":"rename","new_value":"Kick"},`+
			`{"index":2,"expected_name":"Bass","operation":"color","new_value":"red"}]}`)
	if err != nil {
		t.Fatalf("propose = %v", err)
	}
	if !strings.Contains(result, `"outcome":"proposed"`) || !strings.Contains(result, `"edit_count":2`) {
		t.Fatalf("propose result = %s", result)
	}
	if runner.calls != 0 {
		t.Fatalf("proposing touched REAPER: %d calls", runner.calls)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/track-plan", nil))
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || body["plan"] == nil {
		t.Fatalf("pending plan = %d %v", recorder.Code, body)
	}
}

func TestProposeTrackEditsRejectsAnUnknownColorName(t *testing.T) {
	handler, _ := planHandler(t, &bulkRunnerStub{})
	ws, _ := handler.store.Get("mine")
	if _, err := ws.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	tools := handler.AgentTools("mine", "agent-1")
	var proposeTool interface {
		Call(context.Context, string) (string, error)
	}
	for _, tool := range tools {
		if tool.Definition().Name == "propose_reaper_track_edits" {
			proposeTool = tool
		}
	}
	if _, err := proposeTool.Call(context.Background(),
		`{"edits":[{"index":1,"expected_name":"Drums","operation":"color","new_value":"mauve"}]}`); err == nil {
		t.Fatal("an unknown color name was accepted")
	}
}

func TestApplyPendingPlanRequiresExplicitConfirmation(t *testing.T) {
	runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: true, AppliedCount: 1}}
	handler, mux := planHandler(t, runner)
	seedPlan(t, handler, []reaper.TrackEdit{reaper.RenameEdit(1, "Drums", "Kick")})

	unconfirmed := httptest.NewRecorder()
	mux.ServeHTTP(unconfirmed, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", nil))
	var body PlanApplyResponse
	if err := json.Unmarshal(unconfirmed.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if unconfirmed.Code != http.StatusConflict || body.Code != "confirmation_required" || runner.calls != 0 {
		t.Fatalf("unconfirmed apply = %d %+v, calls=%d", unconfirmed.Code, body, runner.calls)
	}

	confirmed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(confirmed, request)
	var confirmedBody PlanApplyResponse
	if err := json.Unmarshal(confirmed.Body.Bytes(), &confirmedBody); err != nil {
		t.Fatal(err)
	}
	if confirmed.Code != http.StatusOK || confirmedBody.Outcome != "ok" || runner.calls != 1 {
		t.Fatalf("confirmed apply = %d %+v, calls=%d", confirmed.Code, confirmedBody, runner.calls)
	}
	if confirmedBody.Undo == nil || confirmedBody.Undo.CommandID != "40029" {
		t.Fatalf("plan undo descriptor = %+v", confirmedBody.Undo)
	}
}

func TestApplyPendingPlanProducesExactlyOneUndoStep(t *testing.T) {
	runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: true, AppliedCount: 3}}
	handler, mux := planHandler(t, runner)
	seedPlan(t, handler, []reaper.TrackEdit{
		reaper.RenameEdit(1, "Drums", "Kick"),
		reaper.ColorEdit(2, "Bass", 0),
		reaper.MoveEdit(1, "Drums", 2),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	var body PlanApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AppliedCount != 3 {
		t.Fatalf("applied count = %+v", body)
	}
	// One toast, one command: REAPER's global undo, since the whole plan ran
	// inside a single undo block server-side.
	if body.Undo.CommandID != "40029" || !strings.Contains(body.Undo.Summary, "3") {
		t.Fatalf("plan undo = %+v", body.Undo)
	}
}

func TestApplyPendingPlanGuardFailureAppliesNothingAndReportsFailedTracks(t *testing.T) {
	runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: false, FailedIndices: []int{2}}}
	handler, mux := planHandler(t, runner)
	seedPlan(t, handler, []reaper.TrackEdit{
		reaper.RenameEdit(1, "Drums", "Kick"),
		reaper.MuteEdit(2, "Bass", true),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	var body PlanApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || body.Outcome == "ok" || body.Code != "plan_guard_failed" {
		t.Fatalf("guard failure = %d %+v", recorder.Code, body)
	}
	if len(body.FailedIndices) != 1 || body.FailedIndices[0] != 2 {
		t.Fatalf("failed indices = %+v", body.FailedIndices)
	}
}

func TestApplyPendingPlanRefusesFolderParentMoveBeforeRunnerAndKeepsPlan(t *testing.T) {
	runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: true, AppliedCount: 1}}
	state := reaper.State{
		Connected: true, PlayState: "stopped", TrackCount: 3, FolderDepthAvailable: true,
		Tracks: []reaper.Track{
			{Index: 1, Name: "Folder", FolderDepth: 1},
			{Index: 2, Name: "Child", FolderDepth: 0},
			{Index: 3, Name: "Closer", FolderDepth: -1},
		},
	}
	handler, mux := planHandlerWithState(t, runner, state)
	seedPlan(t, handler, []reaper.TrackEdit{reaper.MoveEdit(1, "Folder", 3)})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	var body PlanApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || body.Code != folderParentMoveCode || body.Outcome == "ok" {
		t.Fatalf("folder-parent plan = %d %+v", recorder.Code, body)
	}
	if runner.calls != 0 || body.Undo != nil {
		t.Fatalf("folder-parent plan reached runner or exposed undo: calls=%d body=%+v", runner.calls, body)
	}
	if _, found := handler.plans.get("mine"); !found {
		t.Fatal("folder-parent refusal discarded the pending plan")
	}
}

func TestApplyPendingPlanMovePreflightRejectsStaleIdentityAndUnknownDepth(t *testing.T) {
	base := reaper.State{
		Connected: true, PlayState: "stopped", TrackCount: 2, FolderDepthAvailable: true,
		Tracks: []reaper.Track{{Index: 1, Name: "Drums"}, {Index: 2, Name: "Bass"}},
	}
	tests := []struct {
		name     string
		state    reaper.State
		wantCode string
	}{
		{name: "stale identity", state: base, wantCode: "plan_guard_failed"},
		{name: "unknown folder depth", state: func() reaper.State {
			unknown := base
			unknown.FolderDepthAvailable = false
			return unknown
		}(), wantCode: folderDepthMissingCode},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &bulkRunnerStub{}
			handler, mux := planHandlerWithState(t, runner, testCase.state)
			expected := "Drums"
			if testCase.name == "stale identity" {
				expected = "Changed"
			}
			seedPlan(t, handler, []reaper.TrackEdit{reaper.MoveEdit(1, expected, 2)})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
			request.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(recorder, request)
			var body PlanApplyResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusConflict || body.Code != testCase.wantCode || runner.calls != 0 {
				t.Fatalf("preflight = %d %+v, calls=%d", recorder.Code, body, runner.calls)
			}
			if _, found := handler.plans.get("mine"); !found {
				t.Fatal("preflight refusal discarded the pending plan")
			}
		})
	}
}

func TestApplyPendingPlanStillAllowsZeroAndNegativeDepthMoves(t *testing.T) {
	state := reaper.State{
		Connected: true, PlayState: "stopped", TrackCount: 3, FolderDepthAvailable: true,
		Tracks: []reaper.Track{
			{Index: 1, Name: "Folder", FolderDepth: 1},
			{Index: 2, Name: "Child", FolderDepth: 0},
			{Index: 3, Name: "Closer", FolderDepth: -1},
		},
	}
	for _, source := range []struct {
		index int
		name  string
	}{
		{index: 2, name: "Child"},
		{index: 3, name: "Closer"},
	} {
		t.Run(source.name, func(t *testing.T) {
			runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: true, AppliedCount: 1}}
			handler, mux := planHandlerWithState(t, runner, state)
			seedPlan(t, handler, []reaper.TrackEdit{reaper.MoveEdit(source.index, source.name, 1)})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
			request.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(recorder, request)
			var body PlanApplyResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || body.Outcome != "ok" || runner.calls != 1 {
				t.Fatalf("supported plan move = %d %+v, calls=%d", recorder.Code, body, runner.calls)
			}
		})
	}
}

func TestApplyPendingPlanFinalLuaRaceRefusalKeepsPlan(t *testing.T) {
	runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{
		Applied: false, Refusal: "folder_parent", FailedIndices: []int{1},
	}}
	handler, mux := planHandler(t, runner)
	seedPlan(t, handler, []reaper.TrackEdit{reaper.MoveEdit(1, "Drums", 2)})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	var body PlanApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || body.Code != folderParentMoveCode || runner.calls != 1 || body.Undo != nil {
		t.Fatalf("final race plan = %d %+v, calls=%d", recorder.Code, body, runner.calls)
	}
	if _, found := handler.plans.get("mine"); !found {
		t.Fatal("final race refusal discarded the pending plan")
	}
}

func TestCancelPendingPlanMakesNoREAPERContact(t *testing.T) {
	runner := &bulkRunnerStub{}
	handler, mux := planHandler(t, runner)
	seedPlan(t, handler, []reaper.TrackEdit{reaper.RenameEdit(1, "Drums", "Kick")})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/mine/reaper/track-plan", nil))
	if recorder.Code != http.StatusOK || runner.calls != 0 {
		t.Fatalf("cancel = %d, calls=%d", recorder.Code, runner.calls)
	}

	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/track-plan", nil))
	var body map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["plan"] != nil {
		t.Fatalf("plan survived cancel: %v", body)
	}
}

func TestApplyPendingPlanWithoutOneReturnsNoPendingPlan(t *testing.T) {
	_, mux := planHandler(t, &bulkRunnerStub{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	var body PlanApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || body.Code != "no_pending_plan" {
		t.Fatalf("apply without a plan = %d %+v", recorder.Code, body)
	}
}

func seedPlan(t *testing.T, handler *Handler, edits []reaper.TrackEdit) {
	t.Helper()
	if _, err := handler.proposeEdits("mine", "agent-1", edits); err != nil {
		t.Fatal(err)
	}
}

// The same boundary rule as the single-edit paths: no Web Remote port,
// endpoint, or filesystem path may reach the browser through the plan
// surface either, success or failure.
func TestPlanResponsesNeverLeakTransportOrPathDetail(t *testing.T) {
	forbidden := []string{"127.0.0.1", "localhost", "/_/", ".ori-reaper", "inbox.lua", "last_receipt", "last_status", ":2307", ":2308"}
	assertClean := func(t *testing.T, body string) {
		t.Helper()
		for _, term := range forbidden {
			if strings.Contains(body, term) {
				t.Fatalf("response leaked %q: %s", term, body)
			}
		}
	}

	t.Run("guard failure and runner unavailable", func(t *testing.T) {
		runner := &bulkRunnerStub{receipt: reaper.BulkReceipt{Applied: false, FailedIndices: []int{2}}}
		handler, mux := planHandler(t, runner)
		seedPlan(t, handler, []reaper.TrackEdit{reaper.RenameEdit(1, "Drums", "Kick")})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
		request.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(recorder, request)
		assertClean(t, recorder.Body.String())

		runner.err = reaper.ErrRunnerUnavailable
		seedPlan(t, handler, []reaper.TrackEdit{reaper.RenameEdit(1, "Drums", "Kick")})
		unavailable := httptest.NewRecorder()
		mux.ServeHTTP(unavailable, request.Clone(request.Context()))
		assertClean(t, unavailable.Body.String())
	})

	t.Run("folder parent refusal", func(t *testing.T) {
		runner := &bulkRunnerStub{}
		handler, mux := planHandlerWithState(t, runner, reaper.State{
			Connected: true, PlayState: "stopped", TrackCount: 2, FolderDepthAvailable: true,
			Tracks: []reaper.Track{{Index: 1, Name: "Folder", FolderDepth: 1}, {Index: 2, Name: "Child"}},
		})
		seedPlan(t, handler, []reaper.TrackEdit{reaper.MoveEdit(1, "Folder", 2)})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
		request.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(recorder, request)
		assertClean(t, recorder.Body.String())
	})

	t.Run("disconnected", func(t *testing.T) {
		runner := &bulkRunnerStub{err: reaper.ErrActionDisconnected}
		handler, mux := planHandler(t, runner)
		seedPlan(t, handler, []reaper.TrackEdit{reaper.RenameEdit(1, "Drums", "Kick")})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/track-plan/apply", strings.NewReader(`{"confirmed":true}`))
		request.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(recorder, request)
		assertClean(t, recorder.Body.String())
	})
}
