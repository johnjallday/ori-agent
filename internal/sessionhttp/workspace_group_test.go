package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func createTestGroup(t *testing.T, handler *Handler, name string) string {
	t.Helper()

	body := `{"name":"` + name + `","kind":"group"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("failed to create group: %d - %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode group response: %v", err)
	}

	folder, ok := resp["folder"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected folder payload in group response")
	}
	if got := folder["kind"]; got != "group" {
		t.Fatalf("expected kind group, got %v", got)
	}

	return folder["id"].(string)
}

func TestCreateGroupDoesNotCreateFolderOnDisk(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "BK Nerds")

	ws, err := handler.store.GetWorkspace(context.Background(), groupID)
	if err != nil {
		t.Fatalf("failed to load created group: %v", err)
	}
	if ws.Kind != session.WorkspaceKindGroup {
		t.Fatalf("expected group kind, got %q", ws.Kind)
	}

	groupPath := filepath.Join(baseDir, "bk-nerds")
	if _, err := os.Stat(groupPath); !os.IsNotExist(err) {
		t.Fatalf("expected no filesystem folder for group, stat err=%v", err)
	}
}

func TestListWorkspacesFlatExcludesGroups(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+childID, bytes.NewBufferString(`{"parent_id":"`+groupID+`"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleWorkspaces(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("expected 200 when moving workspace into group, got %d: %s", updateW.Code, updateW.Body.String())
	}

	flatReq := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	flatW := httptest.NewRecorder()
	handler.HandleWorkspaces(flatW, flatReq)
	if flatW.Code != http.StatusOK {
		t.Fatalf("expected 200 for flat list, got %d: %s", flatW.Code, flatW.Body.String())
	}

	var flatResp map[string]interface{}
	if err := json.Unmarshal(flatW.Body.Bytes(), &flatResp); err != nil {
		t.Fatalf("failed to decode flat list response: %v", err)
	}
	flatWorkspaces, ok := flatResp["workspaces"].([]interface{})
	if !ok {
		t.Fatalf("expected workspaces list in flat response")
	}
	if len(flatWorkspaces) != 1 {
		t.Fatalf("expected 1 concrete workspace in flat list, got %d", len(flatWorkspaces))
	}

	flatWorkspace := flatWorkspaces[0].(map[string]interface{})
	if got := flatWorkspace["id"]; got != childID {
		t.Fatalf("expected flat list to contain child workspace %q, got %v", childID, got)
	}

	treeReq := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	treeW := httptest.NewRecorder()
	handler.HandleWorkspaces(treeW, treeReq)
	if treeW.Code != http.StatusOK {
		t.Fatalf("expected 200 for tree list, got %d: %s", treeW.Code, treeW.Body.String())
	}

	var treeResp map[string]interface{}
	if err := json.Unmarshal(treeW.Body.Bytes(), &treeResp); err != nil {
		t.Fatalf("failed to decode tree response: %v", err)
	}
	treeWorkspaces, ok := treeResp["workspaces"].([]interface{})
	if !ok || len(treeWorkspaces) != 1 {
		t.Fatalf("expected one root group in tree response, got %#v", treeResp["workspaces"])
	}
	root := treeWorkspaces[0].(map[string]interface{})
	if got := root["kind"]; got != "group" {
		t.Fatalf("expected root kind group, got %v", got)
	}
	children, ok := root["children"].([]interface{})
	if !ok || len(children) != 1 {
		t.Fatalf("expected group to contain one child, got %#v", root["children"])
	}
}

func TestCreateWorkspaceRejectsNonGroupParent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	parentID := createTestWorkspace(t, handler, "Standalone Workspace")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(`{"name":"Child Workspace","parent_id":"`+parentID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-group parent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSessionRejectsGroupAssignment(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	groupID := createTestGroup(t, handler, "Ops")

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"title":"Grouped Session","folder_id":"`+groupID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleSessions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for group session assignment, got %d: %s", w.Code, w.Body.String())
	}
}
