package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func TestHandleWorkspaceSettings_GetReturnsNormalizedDefaults(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	workspaceID := createTestWorkspace(t, handler, "Settings Test")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/settings", nil)
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	settings, ok := response["settings"].(map[string]any)
	if !ok {
		t.Fatalf("expected settings object, got %#v", response["settings"])
	}
	if got := settings["profile"]; got != "general" {
		t.Fatalf("expected general profile, got %#v", got)
	}
	if got := settings["preset"]; got != "guided" {
		t.Fatalf("expected guided preset, got %#v", got)
	}

	effective, ok := response["effective_behavior"].(map[string]any)
	if !ok {
		t.Fatalf("expected effective_behavior object, got %#v", response["effective_behavior"])
	}
	if _, ok := effective["summary"].([]any); !ok {
		t.Fatalf("expected summary array, got %#v", effective["summary"])
	}
}

func TestHandleWorkspaceSettings_PatchPersistsSettings(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	workspaceID := createTestWorkspace(t, handler, "Planner Workspace")

	body := `{
		"profile": "software_project",
		"preset": "planner",
		"workflow": {
			"confirmation_mode": "always"
		},
		"planning": {
			"tasks_dir": "plans"
		}
	}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID+"/settings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	settings := workspacesettings.Extract(workspace.SharedData)
	if settings.Profile != "software_project" {
		t.Fatalf("expected software_project profile, got %q", settings.Profile)
	}
	if settings.Preset != "planner" {
		t.Fatalf("expected planner preset, got %q", settings.Preset)
	}
	if settings.Workflow.ConfirmationMode != "always" {
		t.Fatalf("expected confirmation mode always, got %q", settings.Workflow.ConfirmationMode)
	}
	if settings.Planning.TasksDir != "plans" {
		t.Fatalf("expected tasks dir plans, got %q", settings.Planning.TasksDir)
	}
	if !settings.Planning.Enabled {
		t.Fatal("expected planner preset to enable planning")
	}
}

func TestHandleWorkspaceSettings_PatchPersistsTaskMarkdownSettings(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	workspaceID := createTestWorkspace(t, handler, "Markdown Tasks Workspace")

	body := `{
		"task_markdown": {
			"enabled": true,
			"path": "planning/tasks.md",
			"generate_agent_views": false
		}
	}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID+"/settings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	workspace, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	settings := workspacesettings.Extract(workspace.SharedData)
	if !settings.TaskMarkdown.Enabled {
		t.Fatal("expected task markdown sync enabled")
	}
	if settings.TaskMarkdown.Path != "planning/tasks.md" {
		t.Fatalf("expected planning/tasks.md path, got %q", settings.TaskMarkdown.Path)
	}
	if settings.TaskMarkdown.GenerateAgentViews {
		t.Fatal("expected generated agent views disabled")
	}
}

func TestHandleWorkspaceSettings_RejectsInvalidTaskMarkdownPath(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	workspaceID := createTestWorkspace(t, handler, "Invalid Markdown Tasks Workspace")

	body := `{
		"task_markdown": {
			"enabled": true,
			"path": "../tasks.md"
		}
	}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID+"/settings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
