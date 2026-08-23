package reaperhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// trackRunnerStub stands in for the real runner. It records the edits it was
// asked to apply and replays a scripted sequence of receipts, so a test can
// describe "the guard refused" without a live REAPER.
type trackRunnerStub struct {
	available bool
	receipts  []reaper.EditReceipt
	errs      []error
	edits     []reaper.TrackEdit
	calls     int
}

func (r *trackRunnerStub) Available(context.Context) bool { return r.available }

func (r *trackRunnerStub) RunTrackEdit(_ context.Context, edit reaper.TrackEdit) (reaper.EditReceipt, error) {
	r.edits = append(r.edits, edit)
	index := r.calls
	r.calls++
	var err error
	if index < len(r.errs) {
		err = r.errs[index]
	}
	if err != nil {
		return reaper.EditReceipt{}, err
	}
	if index < len(r.receipts) {
		return r.receipts[index], nil
	}
	return reaper.EditReceipt{Applied: true}, nil
}

func trackEditHandler(t *testing.T, runner TrackEditRunner) *http.ServeMux {
	t.Helper()
	return trackEditHandlerWithState(t, runner, reaper.State{
		Connected:            true,
		PlayState:            "stopped",
		Tracks:               []reaper.Track{{Index: 1, Name: "Drums"}, {Index: 2, Name: "Bass"}},
		TrackCount:           2,
		FolderDepthAvailable: true,
	})
}

func trackEditHandlerWithState(t *testing.T, runner TrackEditRunner, state reaper.State) *http.ServeMux {
	t.Helper()
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{state: state}, nil)
	if runner != nil {
		handler.SetTrackEditRunner(runner)
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	return mux
}

func postTrackEdit(t *testing.T, mux *http.ServeMux, path, body string) (*httptest.ResponseRecorder, TrackEditResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(http.MethodPost, path, nil)
	} else {
		request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	mux.ServeHTTP(recorder, request)
	var decoded TrackEditResponse
	if recorder.Body.Len() > 0 {
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	}
	return recorder, decoded
}

func TestRenameTrackAppliesTheGuardedEditAndStoresAnUndoRecord(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: true, Prior: "Drums"}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
		`{"name":"Kick","expected_name":"Drums"}`)
	if recorder.Code != http.StatusOK || body.Outcome != "ok" {
		t.Fatalf("rename = %d %+v", recorder.Code, body)
	}
	if len(runner.edits) != 1 {
		t.Fatalf("runner edits = %+v", runner.edits)
	}
	edit := runner.edits[0]
	if edit.Index != 1 || edit.ExpectedName != "Drums" || edit.NewName != "Kick" {
		t.Fatalf("guarded edit = %+v", edit)
	}
	if body.Undo == nil || !strings.Contains(body.Undo.Summary, "Kick") {
		t.Fatalf("undo descriptor = %+v", body.Undo)
	}
	if !body.TrackEditingAvailable {
		t.Fatalf("successful edit did not report track editing available: %+v", body)
	}
}

func TestRenameTrackRejectsAnEmptyNameWithoutTouchingREAPER(t *testing.T) {
	runner := &trackRunnerStub{available: true}
	mux := trackEditHandler(t, runner)

	for _, body := range []string{`{"name":"","expected_name":"Drums"}`, `{"name":"   ","expected_name":"Drums"}`} {
		recorder, decoded := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename", body)
		if recorder.Code != http.StatusBadRequest || decoded.Code != "invalid_track_name" {
			t.Fatalf("empty name = %d %+v", recorder.Code, decoded)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("an invalid name reached REAPER: %d calls", runner.calls)
	}
}

func TestRenameTrackRejectsAnInvalidIndexWithoutTouchingREAPER(t *testing.T) {
	runner := &trackRunnerStub{available: true}
	mux := trackEditHandler(t, runner)

	for _, path := range []string{
		"/api/workspaces/mine/reaper/tracks/0/rename",
		"/api/workspaces/mine/reaper/tracks/-1/rename",
		"/api/workspaces/mine/reaper/tracks/master/rename",
	} {
		recorder, _ := postTrackEdit(t, mux, path, `{"name":"Kick","expected_name":"Drums"}`)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("index path %q = %d", path, recorder.Code)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("an invalid index reached REAPER: %d calls", runner.calls)
	}
}

func TestRenameTrackReportsAGuardFailureAndWritesNothing(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: false}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
		`{"name":"Kick","expected_name":"Drums"}`)
	if recorder.Code != http.StatusConflict || body.Code != "track_list_changed" {
		t.Fatalf("guard failure = %d %+v", recorder.Code, body)
	}
	if body.Outcome == "ok" {
		t.Fatalf("a refused guard was reported as success: %+v", body)
	}
	// A refused guard leaves nothing to undo.
	undo, undoBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if undo.Code != http.StatusConflict || undoBody.Code != "nothing_to_undo" {
		t.Fatalf("undo after guard failure = %d %+v", undo.Code, undoBody)
	}
}

func TestUndoAppliesTheSpecificInverseGuardedOnWhatOriWrote(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts: []reaper.EditReceipt{
			{Applied: true, Prior: "Drums"}, // the rename
			{Applied: true, Prior: "Kick"},  // the inverse
		},
	}
	mux := trackEditHandler(t, runner)

	if recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
		`{"name":"Kick","expected_name":"Drums"}`); recorder.Code != http.StatusOK {
		t.Fatalf("rename = %d %+v", recorder.Code, body)
	}
	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if recorder.Code != http.StatusOK || body.Outcome != "ok" {
		t.Fatalf("undo = %d %+v", recorder.Code, body)
	}
	if len(runner.edits) != 2 {
		t.Fatalf("runner edits = %+v", runner.edits)
	}
	inverse := runner.edits[1]
	// The inverse restores the prior name and guards on the value Ori wrote,
	// so a user edit in between is refused rather than clobbered.
	if inverse.Index != 1 || inverse.ExpectedName != "Kick" || inverse.NewName != "Drums" {
		t.Fatalf("inverse = %+v", inverse)
	}
	// The record is consumed, so a second Undo click cannot re-apply it.
	second, secondBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if second.Code != http.StatusConflict || secondBody.Code != "nothing_to_undo" {
		t.Fatalf("second undo = %d %+v", second.Code, secondBody)
	}
}

func TestUndoReportsWhenTheTrackChangedInREAPER(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts: []reaper.EditReceipt{
			{Applied: true, Prior: "Drums"}, // the rename
			{Applied: false},                // the user renamed it in REAPER first
		},
	}
	mux := trackEditHandler(t, runner)

	postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename", `{"name":"Kick","expected_name":"Drums"}`)
	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if recorder.Code != http.StatusConflict || body.Code != "track_changed_in_reaper" {
		t.Fatalf("undo guard failure = %d %+v", recorder.Code, body)
	}
	if !strings.Contains(body.ErrorReason, "nothing was undone") {
		t.Fatalf("undo guard message = %q", body.ErrorReason)
	}
}

func TestTrackEditReportsDisconnectedREAPERAsNothingRun(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		errs:      []error{reaper.ErrActionDisconnected},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
		`{"name":"Kick","expected_name":"Drums"}`)
	if recorder.Code != http.StatusConflict || body.Code != "reaper_disconnected" {
		t.Fatalf("disconnected edit = %d %+v", recorder.Code, body)
	}
	if !strings.Contains(body.ErrorReason, "Nothing was run") || body.Outcome == "ok" {
		t.Fatalf("disconnected edit message = %+v", body)
	}
}

func TestTrackEditIsUnavailableWithoutTheRunner(t *testing.T) {
	mux := trackEditHandler(t, nil)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
		`{"name":"Kick","expected_name":"Drums"}`)
	if recorder.Code != http.StatusServiceUnavailable || body.Code != "reaper_runner_unavailable" {
		t.Fatalf("missing runner = %d %+v", recorder.Code, body)
	}
}

func TestStateReportsWhetherTrackEditingIsAvailable(t *testing.T) {
	for _, available := range []bool{true, false} {
		runner := &trackRunnerStub{available: available}
		mux := trackEditHandler(t, runner)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/state", nil))
		var state reaper.State
		if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.TrackEditingAvailable != available {
			t.Fatalf("track_editing_available = %v, want %v", state.TrackEditingAvailable, available)
		}
	}
}

func TestColorTrackAppliesTheGuardedEditAndStoresAnUndoRecord(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: true, Prior: "0"}},
	}
	mux := trackEditHandler(t, runner)

	color := int64(0x1000000 | 0xff0000)
	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/color",
		`{"color":`+strconv.FormatInt(color, 10)+`,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusOK || body.Outcome != "ok" {
		t.Fatalf("color = %d %+v", recorder.Code, body)
	}
	if len(runner.edits) != 1 {
		t.Fatalf("runner edits = %+v", runner.edits)
	}
	edit := runner.edits[0]
	if edit.Kind != reaper.TrackEditColor || edit.Index != 1 || edit.ExpectedName != "Drums" || edit.NewColor != color {
		t.Fatalf("guarded edit = %+v", edit)
	}
	if body.Undo == nil || !strings.Contains(body.Undo.Summary, "Recolored") {
		t.Fatalf("undo descriptor = %+v", body.Undo)
	}

	// Undo restores the prior color, guarded on the name the color edit left.
	undoRecorder, undoBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if undoRecorder.Code != http.StatusOK || undoBody.Outcome != "ok" {
		t.Fatalf("undo = %d %+v", undoRecorder.Code, undoBody)
	}
	inverse := runner.edits[1]
	if inverse.Kind != reaper.TrackEditColor || inverse.NewColor != 0 || inverse.ExpectedName != "Drums" {
		t.Fatalf("color inverse = %+v", inverse)
	}
}

func TestColorTrackRejectsAnOutOfRangeColorWithoutTouchingREAPER(t *testing.T) {
	runner := &trackRunnerStub{available: true}
	mux := trackEditHandler(t, runner)

	for _, color := range []string{"-1", "16777215", "50331647"} { // missing flag bit, or stray high bits
		recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/color",
			`{"color":`+color+`,"expected_name":"Drums"}`)
		if recorder.Code != http.StatusBadRequest || body.Code != "invalid_track_edit" {
			t.Fatalf("color %s = %d %+v", color, recorder.Code, body)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("an invalid color reached REAPER: %d calls", runner.calls)
	}
}

func TestToggleEndpointsApplyGuardedEditsAndStoreUndo(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		kind    string
		summary string
	}{
		{"mute", "mute", reaper.TrackEditMute, "Muted"},
		{"solo", "solo", reaper.TrackEditSolo, "Soloed"},
		{"arm", "arm", reaper.TrackEditArm, "Armed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &trackRunnerStub{
				available: true,
				receipts:  []reaper.EditReceipt{{Applied: true, Prior: "0"}},
			}
			mux := trackEditHandler(t, runner)

			recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/"+testCase.path,
				`{"value":true,"expected_name":"Drums"}`)
			if recorder.Code != http.StatusOK || body.Outcome != "ok" {
				t.Fatalf("%s = %d %+v", testCase.name, recorder.Code, body)
			}
			edit := runner.edits[0]
			if edit.Kind != testCase.kind || edit.Index != 1 || edit.ExpectedName != "Drums" || !edit.NewBool {
				t.Fatalf("guarded edit = %+v", edit)
			}
			if body.Undo == nil || !strings.Contains(body.Undo.Summary, testCase.summary) {
				t.Fatalf("undo descriptor = %+v", body.Undo)
			}
		})
	}
}

func TestToggleEndpointGuardFailureAppliesNothing(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: false}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/mute",
		`{"value":true,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusConflict || body.Code != "track_list_changed" || body.Outcome == "ok" {
		t.Fatalf("mute guard failure = %d %+v", recorder.Code, body)
	}
}

func TestMoveTrackAppliesTheGuardedEditAndStoresAnUndoRecord(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: true, Prior: "1"}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":2,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusOK || body.Outcome != "ok" {
		t.Fatalf("move = %d %+v", recorder.Code, body)
	}
	edit := runner.edits[0]
	if edit.Kind != reaper.TrackEditMove || edit.Index != 1 || edit.NewIndex != 2 || edit.ExpectedName != "Drums" {
		t.Fatalf("guarded edit = %+v", edit)
	}
	if body.Undo == nil || !strings.Contains(body.Undo.Summary, "Moved") {
		t.Fatalf("undo descriptor = %+v", body.Undo)
	}

	// Undo restores the prior position, guarded on the name at the new spot.
	undoRecorder, undoBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if undoRecorder.Code != http.StatusOK || undoBody.Outcome != "ok" {
		t.Fatalf("undo = %d %+v", undoRecorder.Code, undoBody)
	}
	inverse := runner.edits[1]
	if inverse.Kind != reaper.TrackEditMove || inverse.Index != 2 || inverse.NewIndex != 1 || inverse.ExpectedName != "Drums" {
		t.Fatalf("move inverse = %+v", inverse)
	}
}

func TestMoveTrackRejectsAnOutOfRangeTargetBeforeGeneratingLua(t *testing.T) {
	runner := &trackRunnerStub{available: true}
	mux := trackEditHandler(t, runner)

	// The stub state has two tracks, so position 5 is out of range.
	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":5,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusBadRequest || body.Code != "invalid_track_edit" {
		t.Fatalf("out-of-range move = %d %+v", recorder.Code, body)
	}
	if runner.calls != 0 {
		t.Fatalf("an out-of-range move reached REAPER: %d calls", runner.calls)
	}

	zero, zeroBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":0,"expected_name":"Drums"}`)
	if zero.Code != http.StatusBadRequest || zeroBody.Code != "invalid_track_edit" {
		t.Fatalf("zero target = %d %+v", zero.Code, zeroBody)
	}
	if runner.calls != 0 {
		t.Fatalf("a zero target reached REAPER: %d calls", runner.calls)
	}
}

func TestMoveTrackRefusesFolderParentsBeforeRunnerOrUndo(t *testing.T) {
	runner := &trackRunnerStub{available: true}
	state := reaper.State{
		Connected: true, PlayState: "stopped", TrackCount: 3, FolderDepthAvailable: true,
		Tracks: []reaper.Track{
			{Index: 1, Name: "Folder", FolderDepth: 1},
			{Index: 2, Name: "Child", FolderDepth: 0},
			{Index: 3, Name: "Closer", FolderDepth: -1},
		},
	}
	mux := trackEditHandlerWithState(t, runner, state)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":3,"expected_name":"Folder"}`)
	if recorder.Code != http.StatusConflict || body.Code != folderParentMoveCode || body.Outcome == "ok" {
		t.Fatalf("folder-parent move = %d %+v", recorder.Code, body)
	}
	if runner.calls != 0 || body.Undo != nil || !strings.Contains(body.ErrorReason, "moved in REAPER") {
		t.Fatalf("folder-parent refusal reached mutation or exposed undo: calls=%d body=%+v", runner.calls, body)
	}
	undo, undoBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if undo.Code != http.StatusConflict || undoBody.Code != "nothing_to_undo" {
		t.Fatalf("undo after folder-parent refusal = %d %+v", undo.Code, undoBody)
	}
}

func TestMoveTrackPreflightRejectsStaleIdentityAndUnknownDepth(t *testing.T) {
	base := reaper.State{
		Connected: true, PlayState: "stopped", TrackCount: 2, FolderDepthAvailable: true,
		Tracks: []reaper.Track{{Index: 1, Name: "Drums"}, {Index: 2, Name: "Bass"}},
	}

	t.Run("stale identity", func(t *testing.T) {
		runner := &trackRunnerStub{available: true}
		mux := trackEditHandlerWithState(t, runner, base)
		recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
			`{"new_index":2,"expected_name":"Changed"}`)
		if recorder.Code != http.StatusConflict || body.Code != "track_list_changed" || runner.calls != 0 {
			t.Fatalf("stale move = %d %+v, calls=%d", recorder.Code, body, runner.calls)
		}
	})

	t.Run("unknown folder depth", func(t *testing.T) {
		runner := &trackRunnerStub{available: true}
		unknown := base
		unknown.FolderDepthAvailable = false
		mux := trackEditHandlerWithState(t, runner, unknown)
		recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
			`{"new_index":2,"expected_name":"Drums"}`)
		if recorder.Code != http.StatusConflict || body.Code != folderDepthMissingCode || runner.calls != 0 {
			t.Fatalf("unknown-depth move = %d %+v, calls=%d", recorder.Code, body, runner.calls)
		}
	})
}

func TestMoveTrackStillAllowsZeroAndNegativeDepthSources(t *testing.T) {
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
			runner := &trackRunnerStub{available: true, receipts: []reaper.EditReceipt{{Applied: true, Prior: strconv.Itoa(source.index)}}}
			mux := trackEditHandlerWithState(t, runner, state)
			recorder, body := postTrackEdit(t, mux,
				"/api/workspaces/mine/reaper/tracks/"+strconv.Itoa(source.index)+"/move",
				`{"new_index":1,"expected_name":"`+source.name+`"}`)
			if recorder.Code != http.StatusOK || body.Outcome != "ok" || runner.calls != 1 {
				t.Fatalf("supported move = %d %+v, calls=%d", recorder.Code, body, runner.calls)
			}
		})
	}
}

func TestMoveTrackFinalLuaRaceRefusalIsNotApplied(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: false, Refusal: "folder_parent"}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":2,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusConflict || body.Code != folderParentMoveCode || body.Outcome == "ok" {
		t.Fatalf("final race refusal = %d %+v", recorder.Code, body)
	}
	if runner.calls != 1 || body.Undo != nil {
		t.Fatalf("final race refusal calls=%d undo=%+v", runner.calls, body.Undo)
	}
	undo, undoBody := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/undo", "")
	if undo.Code != http.StatusConflict || undoBody.Code != "nothing_to_undo" {
		t.Fatalf("undo after final race refusal = %d %+v", undo.Code, undoBody)
	}
}

func TestMoveTrackGuardFailureAppliesNothing(t *testing.T) {
	runner := &trackRunnerStub{
		available: true,
		receipts:  []reaper.EditReceipt{{Applied: false}},
	}
	mux := trackEditHandler(t, runner)

	recorder, body := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
		`{"new_index":2,"expected_name":"Drums"}`)
	if recorder.Code != http.StatusConflict || body.Code != "track_list_changed" || body.Outcome == "ok" {
		t.Fatalf("move guard failure = %d %+v", recorder.Code, body)
	}
}

// The boundary rule from the control-surface work: no Web Remote port,
// endpoint, or filesystem path may reach the browser through any track path
// — rename, color, every toggle, move, and undo alike, on both the success
// and the failure side of each.
func TestTrackEditResponsesNeverLeakTransportOrPathDetail(t *testing.T) {
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
		runner := &trackRunnerStub{
			available: true,
			receipts:  []reaper.EditReceipt{{Applied: false}},
			errs:      []error{nil, reaper.ErrRunnerUnavailable},
		}
		mux := trackEditHandler(t, runner)
		for _, call := range []struct{ path, body string }{
			{"/api/workspaces/mine/reaper/tracks/1/rename", `{"name":"Kick","expected_name":"Drums"}`},
			{"/api/workspaces/mine/reaper/tracks/1/rename", `{"name":"Snare","expected_name":"Kick"}`},
			{"/api/workspaces/mine/reaper/tracks/undo", ""},
		} {
			recorder, _ := postTrackEdit(t, mux, call.path, call.body)
			assertClean(t, recorder.Body.String())
		}
	})

	t.Run("color, mute, solo, arm, move", func(t *testing.T) {
		runner := &trackRunnerStub{
			available: true,
			receipts:  []reaper.EditReceipt{{Applied: true, Prior: "0"}},
		}
		mux := trackEditHandler(t, runner)
		for _, call := range []struct{ path, body string }{
			{"/api/workspaces/mine/reaper/tracks/1/color", `{"color":16777471,"expected_name":"Drums"}`},
			{"/api/workspaces/mine/reaper/tracks/1/mute", `{"value":true,"expected_name":"Drums"}`},
			{"/api/workspaces/mine/reaper/tracks/1/solo", `{"value":true,"expected_name":"Drums"}`},
			{"/api/workspaces/mine/reaper/tracks/1/arm", `{"value":true,"expected_name":"Drums"}`},
			{"/api/workspaces/mine/reaper/tracks/1/move", `{"new_index":2,"expected_name":"Drums"}`},
		} {
			recorder, _ := postTrackEdit(t, mux, call.path, call.body)
			assertClean(t, recorder.Body.String())
		}
	})

	t.Run("folder parent refusal", func(t *testing.T) {
		runner := &trackRunnerStub{available: true}
		mux := trackEditHandlerWithState(t, runner, reaper.State{
			Connected: true, PlayState: "stopped", TrackCount: 2, FolderDepthAvailable: true,
			Tracks: []reaper.Track{{Index: 1, Name: "Folder", FolderDepth: 1}, {Index: 2, Name: "Child"}},
		})
		recorder, _ := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/move",
			`{"new_index":2,"expected_name":"Folder"}`)
		assertClean(t, recorder.Body.String())
	})

	t.Run("disconnected", func(t *testing.T) {
		runner := &trackRunnerStub{available: true, errs: []error{reaper.ErrActionDisconnected}}
		mux := trackEditHandler(t, runner)
		recorder, _ := postTrackEdit(t, mux, "/api/workspaces/mine/reaper/tracks/1/rename",
			`{"name":"Kick","expected_name":"Drums"}`)
		assertClean(t, recorder.Body.String())
	})
}
