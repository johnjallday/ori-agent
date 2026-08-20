package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	state  reaper.State
	err    error
	calls  int
	source reaper.ProjectSource
}

func (r *stateReader) ReadState(_ context.Context, source reaper.ProjectSource) (reaper.State, error) {
	r.calls++
	r.source = source
	return r.state, r.err
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
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader)

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
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader)

	recorder, body := serveState(t, handler, "mine")
	if recorder.Code != http.StatusOK || body["applies"] != false || reader.calls != 0 {
		t.Fatalf("non-applicable state = %d %#v, calls = %d", recorder.Code, body, reader.calls)
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
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader)

	recorder, body := serveState(t, handler, "mine")
	if recorder.Code != http.StatusOK || body["applies"] != true || body["connected"] != false || body["reason"] != "reaper_unreachable" {
		t.Fatalf("disconnected state = %d %#v", recorder.Code, body)
	}
	wantPath := filepath.Join(root, "project", "song.rpp")
	if reader.source.Path != wantPath || reader.source.EntryPath != "song.rpp" {
		t.Fatalf("project source = %+v, want path %q", reader.source, wantPath)
	}
}
