package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type testStore struct {
	root       string
	workspaces map[string]*workspace.Workspace
}

func (s *testStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ws, nil
}

func (s *testStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	return s.Get(id)
}

func (s *testStore) GetFolderPath(string) (string, error) { return s.root, nil }

type testUser string

func (u testUser) CurrentUserID(context.Context) (string, error) { return string(u), nil }

type scriptRunnerStub struct {
	result reaper.ScriptRunResult
	err    error
	calls  int
	lua    string
}

func (r *scriptRunnerStub) RunScript(_ context.Context, lua string) (reaper.ScriptRunResult, error) {
	r.calls++
	r.lua = lua
	return r.result, r.err
}

type stateReader struct {
	state       reaper.State
	err         error
	calls       int
	source      reaper.ProjectSource
	runState    reaper.State
	runErr      error
	runCalls    int
	runActionID string
}

func (r *stateReader) ReadState(_ context.Context, source reaper.ProjectSource) (reaper.State, error) {
	r.calls++
	r.source = source
	return r.state, r.err
}

func (r *stateReader) RunAction(_ context.Context, actionID string, source reaper.ProjectSource) (reaper.State, error) {
	r.runCalls++
	r.runActionID = actionID
	r.source = source
	return r.runState, r.runErr
}

func reaperHTTPWorkspace(t *testing.T, root, id, owner string) *workspace.Workspace {
	t.Helper()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "song.rpp"), []byte("<REAPER_PROJECT\nTEMPO 120 4 4\n>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws.ID = id
	ws.OwnerUserID = owner
	ws.ProjectPath = "project"
	ws.SharedData = map[string]any{}
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "not-used-for-gating",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{
				{ID: "file_only", Label: "File only", Description: "Use files."},
				{ID: "ori_assisted", Label: "Assisted", Description: "Use REAPER.", Requires: []string{"reaper_live_control"}},
			},
			Requirements: []workspace.RuntimeRequirement{{
				Key: "reaper_live_control", Label: "REAPER", Description: "Use REAPER.", Adapter: "reaper_live_control",
			}},
		},
	})
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "ori_assisted"})
	return ws
}

func serveState(t *testing.T, handler *Handler, workspaceID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	handler.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/reaper/state", nil))
	var body map[string]any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v: %s", err, recorder.Body.String())
		}
	}
	return recorder, body
}

func TestGetStateRejectsWorkspaceCurrentUserDoesNotOwn(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["theirs"] = reaperHTTPWorkspace(t, root, "theirs", "someone-else")
	reader := &stateReader{}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)

	recorder, _ := serveState(t, handler, "theirs")
	if recorder.Code != http.StatusNotFound || reader.calls != 0 {
		t.Fatalf("foreign workspace = %d, calls = %d", recorder.Code, reader.calls)
	}
}

func TestGetStateDoesNotProbeWorkspaceWithoutPersistedLiveRuntimeSelection(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	ws := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "file_only"})
	store.workspaces["mine"] = ws
	reader := &stateReader{}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)

	recorder, body := serveState(t, handler, "mine")
	if recorder.Code != http.StatusOK || body["applies"] != false || reader.calls != 0 {
		t.Fatalf("non-applicable state = %d %#v, calls = %d", recorder.Code, body, reader.calls)
	}
}

func TestGetActionsReturnsCuratedCatalog(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{}, nil)
	mux := http.NewServeMux()
	handler.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/actions", nil))
	var actions []reaper.Action
	if err := json.Unmarshal(recorder.Body.Bytes(), &actions); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(actions) != 9 || actions[0].ID != "1007" || actions[0].NeedsConfirmation {
		t.Fatalf("catalog = %d, %+v", recorder.Code, actions)
	}
}

func TestGetActionsIncludesRegisteredReaScripts(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	keyboardConfig := filepath.Join(root, "reaper-kb.ini")
	if err := os.WriteFile(keyboardConfig, []byte(`SCR 4 0 RSdeadBEEF "Custom: Add markers.lua" Scripts/add-markers.lua`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{}, reaper.NewCatalogWithKeyboardConfig(keyboardConfig))
	mux := http.NewServeMux()
	handler.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/actions", nil))
	var actions []reaper.Action
	if err := json.Unmarshal(recorder.Body.Bytes(), &actions); err != nil {
		t.Fatal(err)
	}
	registered := actions[len(actions)-1]
	if recorder.Code != http.StatusOK || registered.ID != "_RSdeadBEEF" || registered.Label != "Add markers.lua" || registered.Source != reaper.ActionSourceRegistered {
		t.Fatalf("registered catalog = %d %+v", recorder.Code, actions)
	}
}

func TestRunActionValidatesRawIDBeforeClientAndAlwaysConfirmsIt(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	reader := &stateReader{runState: reaper.State{Connected: true, PlayState: "stopped", Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)
	mux := http.NewServeMux()
	handler.Register(mux)

	invalid := httptest.NewRecorder()
	mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/not-a-command/run", nil))
	if invalid.Code != http.StatusBadRequest || reader.runCalls != 0 {
		t.Fatalf("invalid raw id = %d %s, calls=%d", invalid.Code, invalid.Body.String(), reader.runCalls)
	}

	unconfirmed := httptest.NewRecorder()
	mux.ServeHTTP(unconfirmed, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/_RSdeadBEEF/run", nil))
	var body ActionRunResponse
	if err := json.Unmarshal(unconfirmed.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if unconfirmed.Code != http.StatusConflict || body.Outcome != "confirmation_required" || reader.runCalls != 0 {
		t.Fatalf("unconfirmed raw = %d %+v, calls=%d", unconfirmed.Code, body, reader.runCalls)
	}

	confirmed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/_RSdeadBEEF/run", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(confirmed, request)
	if confirmed.Code != http.StatusOK || reader.runCalls != 1 || reader.runActionID != "_RSdeadBEEF" {
		t.Fatalf("confirmed raw = %d %s, reader=%+v", confirmed.Code, confirmed.Body.String(), reader)
	}
}

func TestRunActionReturnsResultingStateAndEnforcesConfirmation(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	reader := &stateReader{runState: reaper.State{Connected: true, PlayState: "playing", Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)
	mux := http.NewServeMux()
	handler.Register(mux)

	play := httptest.NewRecorder()
	mux.ServeHTTP(play, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/1007/run", nil))
	var playBody ActionRunResponse
	if err := json.Unmarshal(play.Body.Bytes(), &playBody); err != nil {
		t.Fatal(err)
	}
	if play.Code != http.StatusOK || playBody.Outcome != "ok" || !playBody.Connected || reader.runCalls != 1 || reader.runActionID != "1007" {
		t.Fatalf("play = %d %+v, reader=%+v", play.Code, playBody, reader)
	}

	insert := httptest.NewRecorder()
	mux.ServeHTTP(insert, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/40001/run", nil))
	var insertBody ActionRunResponse
	if err := json.Unmarshal(insert.Body.Bytes(), &insertBody); err != nil {
		t.Fatal(err)
	}
	if insert.Code != http.StatusConflict || insertBody.Outcome != "confirmation_required" || reader.runCalls != 1 {
		t.Fatalf("unconfirmed insert = %d %+v, calls=%d", insert.Code, insertBody, reader.runCalls)
	}

	confirmed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/40001/run", strings.NewReader(`{"confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(confirmed, request)
	if confirmed.Code != http.StatusOK || reader.runCalls != 2 || reader.runActionID != "40001" {
		t.Fatalf("confirmed insert = %d %s, reader=%+v", confirmed.Code, confirmed.Body.String(), reader)
	}
}

func TestRunActionSurfacesDisconnectedOutcomeWithoutSuccess(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	reader := &stateReader{
		runState: reaper.State{Connected: false, Reason: "reaper_unreachable", Tracks: []reaper.Track{}},
		runErr:   reaper.ErrActionDisconnected,
	}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)
	mux := http.NewServeMux()
	handler.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/1007/run", nil))
	var body ActionRunResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || body.Outcome != "error" || body.ErrorReason == "" || body.Connected {
		t.Fatalf("disconnected run = %d %+v", recorder.Code, body)
	}
}

func TestScriptCRUDJoinsCatalogAndRunsThroughRunner(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	library := reaper.NewLibraryAt(filepath.Join(root, "Ori Scripts", "reaper"))
	catalog := reaper.NewCatalogWithKeyboardConfig("")
	catalog.SetLibrary(library)
	reader := &stateReader{state: reaper.State{Connected: true, PlayState: "stopped", Tracks: []reaper.Track{}}}
	runner := &scriptRunnerStub{result: reaper.ScriptRunResult{Outcome: "ok"}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, catalog)
	handler.SetScriptServices(library, runner)
	mux := http.NewServeMux()
	handler.Register(mux)

	createBody := `{"filename":"band.lua","name":"Add band tracks","description":"Adds band tracks.","needs_confirmation":true,"code":"return 1\\n"}`
	created := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/scripts", strings.NewReader(createBody))
	mux.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/scripts", nil))
	var scripts []reaper.Script
	if err := json.Unmarshal(listed.Body.Bytes(), &scripts); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].ID != "custom:band.lua" || scripts[0].Code != "" {
		t.Fatalf("scripts = %+v", scripts)
	}

	actions := httptest.NewRecorder()
	mux.ServeHTTP(actions, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/actions", nil))
	var catalogActions []reaper.Action
	if err := json.Unmarshal(actions.Body.Bytes(), &catalogActions); err != nil {
		t.Fatal(err)
	}
	custom := catalogActions[len(catalogActions)-1]
	if custom.ID != "custom:band.lua" || custom.Source != reaper.ActionSourceCustom {
		t.Fatalf("custom action = %+v", custom)
	}

	unconfirmed := httptest.NewRecorder()
	mux.ServeHTTP(unconfirmed, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/custom:band.lua/run", nil))
	if unconfirmed.Code != http.StatusConflict || runner.calls != 0 {
		t.Fatalf("unconfirmed custom = %d %s, calls=%d", unconfirmed.Code, unconfirmed.Body.String(), runner.calls)
	}
	confirmed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/actions/custom:band.lua/run", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(confirmed, request)
	var run ActionRunResponse
	if err := json.Unmarshal(confirmed.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if confirmed.Code != http.StatusOK || run.Outcome != "ok" || runner.calls != 1 || !strings.Contains(runner.lua, "return 1") {
		t.Fatalf("custom run = %d %+v, runner=%+v", confirmed.Code, run, runner)
	}

	deleted := httptest.NewRecorder()
	mux.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/workspaces/mine/reaper/scripts/custom:band.lua", nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	if _, err := library.Read("band.lua"); !errors.Is(err, reaper.ErrScriptNotFound) {
		t.Fatalf("deleted script remains: %v", err)
	}
}

func TestAgentProposalDraftRunReviewAndSaveLoop(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	ws := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	if _, err := ws.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	store.workspaces["mine"] = ws
	library := reaper.NewLibraryAt(filepath.Join(root, "Ori Scripts", "reaper"))
	catalog := reaper.NewCatalogWithKeyboardConfig("")
	catalog.SetLibrary(library)
	reader := &stateReader{state: reaper.State{Connected: true, Tracks: []reaper.Track{}}}
	runner := &scriptRunnerStub{result: reaper.ScriptRunResult{Outcome: "error", ErrorText: "attempt to call nil value"}, err: reaper.ErrRunnerFailed}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, catalog)
	handler.SetScriptServices(library, runner)
	tools := handler.AgentTools("mine", "agent-1")
	var propose toolapi.Tool
	for _, tool := range tools {
		if tool.Definition().Name == "propose_reaper_script" {
			propose = tool
		}
		if strings.Contains(tool.Definition().Name, "save") {
			t.Fatalf("agent received a script save tool: %s", tool.Definition().Name)
		}
	}
	if propose == nil {
		t.Fatal("proposal tool was not attached")
	}
	proposalResult, err := propose.Call(context.Background(), `{"filename":"layout.lua","name":"Build layout","description":"Adds a track layout.","needs_confirmation":true,"code":"reaper.NoSuchFunction()\\n"}`)
	if err != nil || !strings.Contains(proposalResult, `"outcome":"proposed"`) {
		t.Fatalf("proposal = %q, %v", proposalResult, err)
	}
	proposals := handler.proposals.list("mine")
	if len(proposals) != 1 || proposals[0].TestedSuccessfully {
		t.Fatalf("proposals = %+v", proposals)
	}
	proposalID := proposals[0].ID
	if scripts, err := library.List(); err != nil || len(scripts) != 0 {
		t.Fatalf("draft entered library: %+v, %v", scripts, err)
	}
	if actions, err := catalog.List(); err != nil || len(actions) != len(reaper.BuiltinActions()) {
		t.Fatalf("draft entered catalog: %+v, %v", actions, err)
	}

	mux := http.NewServeMux()
	handler.Register(mux)
	unconfirmed := httptest.NewRecorder()
	mux.ServeHTTP(unconfirmed, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+proposalID+"/run", nil))
	if unconfirmed.Code != http.StatusConflict || runner.calls != 0 {
		t.Fatalf("unconfirmed draft = %d %s, calls=%d", unconfirmed.Code, unconfirmed.Body.String(), runner.calls)
	}
	failed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+proposalID+"/run", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(failed, request)
	var failedRun DraftRunResponse
	if err := json.Unmarshal(failed.Body.Bytes(), &failedRun); err != nil {
		t.Fatal(err)
	}
	if failed.Code != http.StatusBadGateway || failedRun.ErrorText != "attempt to call nil value" || failedRun.TestedSuccessfully {
		t.Fatalf("failed draft = %d %+v", failed.Code, failedRun)
	}

	runner.result = reaper.ScriptRunResult{Outcome: "ok"}
	runner.err = nil
	passed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+proposalID+"/run", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(passed, request)
	var passedRun DraftRunResponse
	if err := json.Unmarshal(passed.Body.Bytes(), &passedRun); err != nil {
		t.Fatal(err)
	}
	if passed.Code != http.StatusOK || !passedRun.TestedSuccessfully {
		t.Fatalf("passed draft = %d %+v", passed.Code, passedRun)
	}
	if scripts, _ := library.List(); len(scripts) != 0 {
		t.Fatalf("successful draft entered library: %+v", scripts)
	}

	saveGate := httptest.NewRecorder()
	mux.ServeHTTP(saveGate, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+proposalID+"/save", nil))
	if saveGate.Code != http.StatusConflict || !strings.Contains(saveGate.Body.String(), "every REAPER workspace") {
		t.Fatalf("save gate = %d %s", saveGate.Code, saveGate.Body.String())
	}
	saved := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+proposalID+"/save", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(saved, request)
	if saved.Code != http.StatusCreated || !strings.Contains(saved.Body.String(), `"tested_successfully":true`) {
		t.Fatalf("save = %d %s", saved.Code, saved.Body.String())
	}
	if scripts, err := library.List(); err != nil || len(scripts) != 1 || scripts[0].Filename != "layout.lua" {
		t.Fatalf("saved library = %+v, %v", scripts, err)
	}
	if remaining := handler.proposals.list("mine"); len(remaining) != 0 {
		t.Fatalf("saved proposal was not removed: %+v", remaining)
	}

	untestedResult, err := propose.Call(context.Background(), `{"filename":"untested.lua","name":"Untested draft","description":"May still be saved.","needs_confirmation":true,"code":"return 2\\n"}`)
	if err != nil || !strings.Contains(untestedResult, `"tested_successfully":false`) {
		t.Fatalf("untested proposal = %q, %v", untestedResult, err)
	}
	untested := handler.proposals.list("mine")
	if len(untested) != 1 {
		t.Fatalf("untested proposals = %+v", untested)
	}
	untestedSave := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/script-proposals/"+untested[0].ID+"/save", strings.NewReader(`{"confirmed":true}`))
	mux.ServeHTTP(untestedSave, request)
	if untestedSave.Code != http.StatusCreated || !strings.Contains(untestedSave.Body.String(), `"tested_successfully":false`) {
		t.Fatalf("untested save = %d %s", untestedSave.Code, untestedSave.Body.String())
	}
}

func TestProposalDiscardIsWorkspaceScopedAndLeavesNoLibraryFile(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	mine := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	other := reaperHTTPWorkspace(t, root, "other", userprofile.LocalUserID)
	if _, err := mine.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	store.workspaces["mine"] = mine
	store.workspaces["other"] = other
	library := reaper.NewLibraryAt(filepath.Join(root, "library"))
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{}, nil)
	handler.SetScriptServices(library, &scriptRunnerStub{})
	proposal, err := handler.proposeScript("mine", "agent-1", reaper.ScriptInput{
		Filename: "discard.lua", Name: "Discard me", Description: "Never save this.", Code: "return 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	foreign := httptest.NewRecorder()
	mux.ServeHTTP(foreign, httptest.NewRequest(http.MethodDelete, "/api/workspaces/other/reaper/script-proposals/"+proposal.ID, nil))
	if foreign.Code != http.StatusNotFound || len(handler.proposals.list("mine")) != 1 {
		t.Fatalf("cross-workspace discard = %d %s", foreign.Code, foreign.Body.String())
	}
	removed := httptest.NewRecorder()
	mux.ServeHTTP(removed, httptest.NewRequest(http.MethodDelete, "/api/workspaces/mine/reaper/script-proposals/"+proposal.ID, nil))
	if removed.Code != http.StatusOK || len(handler.proposals.list("mine")) != 0 {
		t.Fatalf("discard = %d %s", removed.Code, removed.Body.String())
	}
	if scripts, err := library.List(); err != nil || len(scripts) != 0 {
		t.Fatalf("discard wrote a library artifact: %+v, %v", scripts, err)
	}
}

func TestDraftRunRechecksExactAgentGrant(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	ws := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	if _, err := ws.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	store.workspaces["mine"] = ws
	handler := NewHandler(store, testUser(userprofile.LocalUserID), &stateReader{}, nil)
	runner := &scriptRunnerStub{result: reaper.ScriptRunResult{Outcome: "ok"}}
	handler.SetScriptServices(reaper.NewLibraryAt(filepath.Join(root, "library")), runner)
	proposal, err := handler.proposeScript("mine", "agent-1", reaper.ScriptInput{
		Filename: "draft.lua", Name: "Draft", Description: "Test", Code: "return 1", NeedsConfirmation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.RevokeRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	response, status := handler.runProposal(context.Background(), "mine", proposal.ID, true)
	if status != http.StatusForbidden || response.Code != "reaper_grant_required" || runner.calls != 0 {
		t.Fatalf("revoked draft = %d %+v, calls=%d", status, response, runner.calls)
	}
}

func TestAgentToolsRequireExactRuntimeGrantAndShareRunPolicy(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	ws := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	store.workspaces["mine"] = ws
	reader := &stateReader{runState: reaper.State{Connected: true, PlayState: "playing", Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)
	tools := handler.AgentTools("mine", "agent-1")
	if len(tools) != 3 {
		t.Fatalf("agent tools = %d", len(tools))
	}
	if _, err := tools[0].Call(context.Background(), `{}`); !errors.Is(err, ErrAgentRuntimeGrantRequired) {
		t.Fatalf("list without grant error = %v", err)
	}
	if reader.runCalls != 0 {
		t.Fatal("an ungranted tool reached REAPER")
	}
	if _, err := ws.GrantRuntimeCapability("reaper_live_control", "agent-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	listed, err := tools[0].Call(context.Background(), `{}`)
	if err != nil || !strings.Contains(listed, `"id":"1007"`) {
		t.Fatalf("granted list = %q, %v", listed, err)
	}

	var runToolIndex int
	for i, tool := range tools {
		if tool.Definition().Name == "run_reaper_action" {
			runToolIndex = i
		}
	}
	unconfirmed, err := tools[runToolIndex].Call(context.Background(), `{"action_id":"40001"}`)
	if err != nil || !strings.Contains(unconfirmed, `"outcome":"confirmation_required"`) || reader.runCalls != 0 {
		t.Fatalf("agent confirmation = %q, err=%v, calls=%d", unconfirmed, err, reader.runCalls)
	}
	result, err := tools[runToolIndex].Call(context.Background(), `{"action_id":"40001","confirmed":true}`)
	if err != nil || !strings.Contains(result, `"outcome":"ok"`) || reader.runCalls != 1 {
		t.Fatalf("agent run = %q, err=%v, calls=%d", result, err, reader.runCalls)
	}

	otherAgentTools := handler.AgentTools("mine", "agent-2")
	if _, err := otherAgentTools[0].Call(context.Background(), `{}`); !errors.Is(err, ErrAgentRuntimeGrantRequired) {
		t.Fatalf("another agent inherited grant: %v", err)
	}
}

func TestGetStateReturnsDisconnectedAsSuccessfulLiveState(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	checkedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	reader := &stateReader{state: reaper.State{
		Connected: false,
		Reason:    "reaper_unreachable",
		PlayState: "unknown",
		Tracks:    []reaper.Track{},
		CheckedAt: checkedAt,
	}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)

	recorder, body := serveState(t, handler, "mine")
	if recorder.Code != http.StatusOK || body["applies"] != true || body["connected"] != false || body["reason"] != "reaper_unreachable" {
		t.Fatalf("disconnected state = %d %#v", recorder.Code, body)
	}
	wantPath := filepath.Join(root, "project", "song.rpp")
	if reader.source.Path != wantPath || reader.source.EntryPath != "song.rpp" {
		t.Fatalf("project source = %+v, want path %q", reader.source, wantPath)
	}
}
