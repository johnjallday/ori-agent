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

func TestAgentToolsRequireExactRuntimeGrantAndShareRunPolicy(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	ws := reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	store.workspaces["mine"] = ws
	reader := &stateReader{runState: reaper.State{Connected: true, PlayState: "playing", Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, nil)
	tools := handler.AgentTools("mine", "agent-1")
	if len(tools) != 2 {
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
