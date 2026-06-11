package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceTagsPatchNormalizesAndPersists(t *testing.T) {
	handler, _, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	createW, createResp := postCreateWorkspace(t, handler, `{"name":"Tagged Workspace"}`)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected workspace create 201, got %d: %s", createW.Code, createW.Body.String())
	}
	folder, ok := createResp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in create response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id")
	}

	patchW, patchResp := patchWorkspace(t, handler, wsID, `{"tags":[" Music ","music","Client:Acme",""]}`)
	if patchW.Code != http.StatusOK {
		t.Fatalf("expected patch 200, got %d: %s", patchW.Code, patchW.Body.String())
	}
	patchFolder, ok := patchResp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in patch response")
	}
	assertMapTags(t, patchFolder, []string{"music", "client:acme"})

	sessionWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("session GetWorkspace: %v", err)
	}
	assertSliceTags(t, sessionWS.Tags, []string{"music", "client:acme"})

	diskWS, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	assertSliceTags(t, diskWS.Tags, []string{"music", "client:acme"})

	hydrated := handler.hydrateWorkspaceMetadataFromFileStore(&session.Workspace{ID: wsID})
	if hydrated == nil {
		t.Fatal("expected hydrated workspace")
	}
	assertSliceTags(t, hydrated.Tags, []string{"music", "client:acme"})

	getReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID, nil)
	getW := httptest.NewRecorder()
	handler.HandleWorkspaces(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected get 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var getResp map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	assertMapTags(t, getResp, []string{"music", "client:acme"})

	listReq := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	listW := httptest.NewRecorder()
	handler.HandleWorkspaces(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(listW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	workspaces, _ := listResp["workspaces"].([]any)
	if len(workspaces) != 1 {
		t.Fatalf("expected one listed workspace, got %#v", listResp["workspaces"])
	}
	listed, _ := workspaces[0].(map[string]any)
	assertMapTags(t, listed, []string{"music", "client:acme"})
}

func TestWorkspaceTagsPatchPersistsWithoutFileStore(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	createW, createResp := postCreateWorkspace(t, handler, `{"name":"Database Only"}`)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected workspace create 201, got %d: %s", createW.Code, createW.Body.String())
	}
	folder, ok := createResp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in create response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id")
	}

	patchW, patchResp := patchWorkspace(t, handler, wsID, `{"tags":["Writing","research"]}`)
	if patchW.Code != http.StatusOK {
		t.Fatalf("expected patch 200, got %d: %s", patchW.Code, patchW.Body.String())
	}
	patchFolder, ok := patchResp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in patch response")
	}
	assertMapTags(t, patchFolder, []string{"writing", "research"})

	loaded, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("session GetWorkspace: %v", err)
	}
	assertSliceTags(t, loaded.Tags, []string{"writing", "research"})
}

func TestWorkspaceTagsPatchRejectsInvalidTags(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	createW, createResp := postCreateWorkspace(t, handler, `{"name":"Invalid Tags"}`)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected workspace create 201, got %d: %s", createW.Code, createW.Body.String())
	}
	folder, ok := createResp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in create response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id")
	}

	overlong := strings.Repeat("x", agentworkspace.MaxWorkspaceTagLength+1)
	overlongW, _ := patchWorkspace(t, handler, wsID, `{"tags":["`+overlong+`"]}`)
	if overlongW.Code != http.StatusBadRequest {
		t.Fatalf("expected overlong tag 400, got %d: %s", overlongW.Code, overlongW.Body.String())
	}

	tags := make([]string, agentworkspace.MaxWorkspaceTags+1)
	for i := range tags {
		tags[i] = "tag-" + string(rune('a'+i))
	}
	bodyBytes, err := json.Marshal(map[string]any{"tags": tags})
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	tooManyW, _ := patchWorkspace(t, handler, wsID, string(bodyBytes))
	if tooManyW.Code != http.StatusBadRequest {
		t.Fatalf("expected too many tags 400, got %d: %s", tooManyW.Code, tooManyW.Body.String())
	}
}

func patchWorkspace(t *testing.T, handler *Handler, workspaceID string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	var resp map[string]any
	if strings.TrimSpace(w.Body.String()) != "" {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode patch response (%d): %v: %s", w.Code, err, w.Body.String())
		}
	}
	return w, resp
}

func assertMapTags(t *testing.T, payload map[string]any, want []string) {
	t.Helper()
	raw, ok := payload["tags"].([]any)
	if !ok {
		t.Fatalf("tags missing or wrong type: %#v", payload["tags"])
	}
	got := make([]string, 0, len(raw))
	for _, tag := range raw {
		got = append(got, tag.(string))
	}
	assertSliceTags(t, got, want)
}

func assertSliceTags(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}
