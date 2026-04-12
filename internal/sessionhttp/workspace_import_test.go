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
	"time"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandleWorkspaceImportCreatesWorkspaceWithDirectoryReference(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "import-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	body := map[string]interface{}{
		"path":        importDir,
		"entry_point": "create_modal",
		"workspace_bootstrap": map[string]interface{}{
			"goal":         "Build the Q2 presentation",
			"systems":      "Keynote, Finder",
			"capabilities": "Create slides and organize imported assets",
			"context":      "Source files live in the imported folder",
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for import, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	folder, ok := resp["folder"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected folder object in response")
	}

	workspaceID, ok := folder["id"].(string)
	if !ok || workspaceID == "" {
		t.Fatalf("expected workspace id in response")
	}

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to fetch created workspace: %v", err)
	}

	refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("failed to decode directory references: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 directory reference, got %d", len(refs))
	}
	expectedPath, err := normalizeImportPath(importDir)
	if err != nil {
		t.Fatalf("failed to normalize expected path: %v", err)
	}
	if filepath.Clean(refs[0].Path) != filepath.Clean(expectedPath) {
		t.Fatalf("expected directory path %q, got %q", expectedPath, refs[0].Path)
	}

	if ws.SharedData == nil {
		t.Fatalf("expected shared_data for imported workspace")
	}
	if _, ok := ws.SharedData["folder_import"]; !ok {
		t.Fatalf("expected folder_import metadata in shared_data")
	}
	bootstrapRaw, ok := ws.SharedData["workspace_bootstrap"]
	if !ok {
		t.Fatalf("expected workspace_bootstrap metadata in shared_data")
	}
	bootstrapMap, ok := bootstrapRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected workspace_bootstrap to be an object, got %T", bootstrapRaw)
	}
	if bootstrapMap["goal"] != "Build the Q2 presentation" {
		t.Fatalf("expected workspace_bootstrap.goal to persist, got %#v", bootstrapMap["goal"])
	}
	if bootstrapMap["systems"] != "Keynote, Finder" {
		t.Fatalf("expected workspace_bootstrap.systems to persist, got %#v", bootstrapMap["systems"])
	}
}

func TestHandleWorkspaceImportRestoresExportedWorkspaceAgents(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	exportRoot := filepath.Join(t.TempDir(), "spain-export")
	childDir := filepath.Join(exportRoot, agentworkspace.SubWorkspacesDir, "madrid")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create exported workspace folders: %v", err)
	}

	now := time.Now()
	rootWorkspace := &agentworkspace.Workspace{
		ID:         "ws-imported-spain",
		Name:       "Spain",
		FolderSlug: "spain-export",
		Agents:     []string{"Trip Manager"},
		SharedData: map[string]interface{}{"entry_agent_name": "Trip Manager"},
		Status:     agentworkspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		AgentInstances: []agentworkspace.AgentInstance{
			{
				ID:             "trip-manager-1",
				Name:           "Trip Manager",
				InstanceNumber: 1,
				NodeID:         "trip-manager-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
	}
	rootData, err := rootWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("failed to encode root workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportRoot, agentworkspace.WorkspaceConfigFile), rootData, 0644); err != nil {
		t.Fatalf("failed to write root workspace.json: %v", err)
	}

	childWorkspace := &agentworkspace.Workspace{
		ID:         "ws-imported-madrid",
		Name:       "Madrid",
		FolderSlug: "madrid",
		Agents:     []string{"Madrid Planner"},
		SharedData: map[string]interface{}{"entry_agent_name": "Madrid Planner"},
		Status:     agentworkspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		AgentInstances: []agentworkspace.AgentInstance{
			{
				ID:             "madrid-planner-1",
				Name:           "Madrid Planner",
				InstanceNumber: 1,
				NodeID:         "madrid-planner-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
	}
	childData, err := childWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("failed to encode child workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, agentworkspace.WorkspaceConfigFile), childData, 0644); err != nil {
		t.Fatalf("failed to write child workspace.json: %v", err)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"path": exportRoot,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for exported workspace restore, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	folder, ok := resp["folder"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected folder object in response")
	}
	if got := folder["id"]; got != rootWorkspace.ID {
		t.Fatalf("expected restored workspace id %q, got %#v", rootWorkspace.ID, got)
	}

	restoredRoot, err := handler.store.GetWorkspace(context.Background(), rootWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch restored root workspace: %v", err)
	}
	if len(restoredRoot.AgentInstances) != 1 {
		t.Fatalf("expected 1 root agent instance, got %d", len(restoredRoot.AgentInstances))
	}
	if restoredRoot.AgentInstances[0].Name != "Trip Manager" {
		t.Fatalf("expected root agent Trip Manager, got %#v", restoredRoot.AgentInstances[0].Name)
	}
	if got := currentWorkspaceEntryAgentName(restoredRoot); got != "Trip Manager" {
		t.Fatalf("expected restored root entry agent Trip Manager, got %q", got)
	}
	if _, ok := restoredRoot.SharedData["folder_import"]; ok {
		t.Fatalf("expected restored workspace to avoid folder_import metadata, got %#v", restoredRoot.SharedData["folder_import"])
	}

	restoredChild, err := handler.store.GetWorkspace(context.Background(), childWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch restored child workspace: %v", err)
	}
	if restoredChild.ParentID != rootWorkspace.ID {
		t.Fatalf("expected restored child parent %q, got %q", rootWorkspace.ID, restoredChild.ParentID)
	}
	if len(restoredChild.AgentInstances) != 1 {
		t.Fatalf("expected 1 child agent instance, got %d", len(restoredChild.AgentInstances))
	}
	if restoredChild.AgentInstances[0].Name != "Madrid Planner" {
		t.Fatalf("expected child agent Madrid Planner, got %#v", restoredChild.AgentInstances[0].Name)
	}
	if got := currentWorkspaceEntryAgentName(restoredChild); got != "Madrid Planner" {
		t.Fatalf("expected restored child entry agent Madrid Planner, got %q", got)
	}
}

func TestHandleWorkspaceImportDuplicateCheckAndConflict(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "duplicate-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	createPayload, _ := json.Marshal(map[string]interface{}{
		"name": "First Import",
		"path": importDir,
	})
	firstReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(createPayload))
	firstReq.Header.Set("Content-Type", "application/json")
	firstW := httptest.NewRecorder()
	handler.HandleWorkspaces(firstW, firstReq)
	if firstW.Code != http.StatusCreated {
		t.Fatalf("expected first import to succeed, got %d: %s", firstW.Code, firstW.Body.String())
	}

	checkReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/import/check?path="+importDir, nil)
	checkW := httptest.NewRecorder()
	handler.HandleWorkspaces(checkW, checkReq)
	if checkW.Code != http.StatusOK {
		t.Fatalf("expected duplicate check 200, got %d: %s", checkW.Code, checkW.Body.String())
	}

	var checkResp map[string]interface{}
	if err := json.Unmarshal(checkW.Body.Bytes(), &checkResp); err != nil {
		t.Fatalf("failed to decode duplicate check response: %v", err)
	}
	dupMap, ok := checkResp["duplicate"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected duplicate payload")
	}
	if found, _ := dupMap["found"].(bool); !found {
		t.Fatalf("expected duplicate found=true")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(createPayload))
	secondReq.Header.Set("Content-Type", "application/json")
	secondW := httptest.NewRecorder()
	handler.HandleWorkspaces(secondW, secondReq)
	if secondW.Code != http.StatusConflict {
		t.Fatalf("expected duplicate import conflict 409, got %d: %s", secondW.Code, secondW.Body.String())
	}

	overridePayload, _ := json.Marshal(map[string]interface{}{
		"name":            "Duplicate Override",
		"path":            importDir,
		"allow_duplicate": true,
	})
	overrideReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(overridePayload))
	overrideReq.Header.Set("Content-Type", "application/json")
	overrideW := httptest.NewRecorder()
	handler.HandleWorkspaces(overrideW, overrideReq)
	if overrideW.Code != http.StatusCreated {
		t.Fatalf("expected duplicate override to succeed, got %d: %s", overrideW.Code, overrideW.Body.String())
	}
}

func TestHandleWorkspaceImportDuplicateActionTelemetry(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "telemetry-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	validPayload, _ := json.Marshal(map[string]interface{}{
		"action":       "suggestion_accepted",
		"workspace_id": "workspace-123",
		"entry_point":  "dashboard_button",
		"path":         importDir,
	})
	validReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import/duplicate-action", bytes.NewBuffer(validPayload))
	validReq.Header.Set("Content-Type", "application/json")
	validW := httptest.NewRecorder()
	handler.HandleWorkspaces(validW, validReq)
	if validW.Code != http.StatusOK {
		t.Fatalf("expected duplicate action request to succeed, got %d: %s", validW.Code, validW.Body.String())
	}

	invalidPayload, _ := json.Marshal(map[string]interface{}{
		"action": "not_allowed",
	})
	invalidReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import/duplicate-action", bytes.NewBuffer(invalidPayload))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidW := httptest.NewRecorder()
	handler.HandleWorkspaces(invalidW, invalidReq)
	if invalidW.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid duplicate action to fail with 400, got %d: %s", invalidW.Code, invalidW.Body.String())
	}
}
