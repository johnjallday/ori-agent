package chathttp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// setupProjectToolProvider builds a provider wired with a session workspace,
// an on-disk workspace folder, a one-template library, and an event bus.
func setupProjectToolProvider(t *testing.T, ws *session.Workspace) (*WorkspaceToolProvider, string, <-chan workspace.Event, func()) {
	t.Helper()
	ctx := context.Background()

	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	if err := sessionStore.CreateWorkspace(ctx, ws); err != nil {
		cleanup()
		t.Fatalf("failed to create workspace: %v", err)
	}

	baseDir := t.TempDir()
	fileStore, err := workspace.NewFileStore(baseDir)
	if err != nil {
		cleanup()
		t.Fatalf("failed to create file store: %v", err)
	}
	if err := fileStore.Save(&workspace.Workspace{
		ID:         ws.ID,
		Name:       ws.Name,
		Kind:       string(ws.Kind),
		FolderSlug: workspace.Slugify(ws.Name),
		Status:     workspace.StatusActive,
	}); err != nil {
		cleanup()
		t.Fatalf("failed to save folder workspace: %v", err)
	}

	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "demo"), 0o750); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "demo", "template.json"), []byte(`{"name":"Demo","description":"demo template"}`), 0o640); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "demo", "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n>\n"), 0o640); err != nil {
		cleanup()
		t.Fatal(err)
	}

	bus := workspace.NewEventBus(10, 10)
	events := make(chan workspace.Event, 10)
	bus.SubscribeToEventType(workspace.EventProjectCreated, func(event workspace.Event) {
		events <- event
	})

	provider := NewWorkspaceToolProvider(sessionStore, nil, ws.ID)
	provider.SetFileStore(fileStore)
	provider.SetProjectTemplateDeps(func() string { return libDir }, bus)

	folderPath, err := fileStore.GetFolderPath(ws.ID)
	if err != nil {
		cleanup()
		t.Fatalf("GetFolderPath: %v", err)
	}
	return provider, folderPath, events, cleanup
}

func TestWorkspaceProjectTemplatesToolLists(t *testing.T) {
	provider, _, _, cleanup := setupProjectToolProvider(t, &session.Workspace{ID: "ws-list", Name: "List"})
	defer cleanup()

	result, err := provider.projectTemplatesTool().Call(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("list tool: %v", err)
	}
	var payload struct {
		Templates []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"templates"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Templates) != 1 || payload.Templates[0].ID != "demo" || payload.Templates[0].Name != "Demo" {
		t.Fatalf("unexpected templates: %+v", payload.Templates)
	}
}

func TestWorkspaceCreateProjectTool(t *testing.T) {
	ctx := context.Background()
	ws := &session.Workspace{ID: "ws-create", Name: "Song X"}
	provider, folderPath, events, cleanup := setupProjectToolProvider(t, ws)
	defer cleanup()

	result, err := provider.createProjectTool().Call(ctx, `{"template_id":"demo"}`)
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	var payload struct {
		ProjectPath string `json:"project_path"`
		TemplateID  string `json:"template_id"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.ProjectPath != "song-x" || payload.TemplateID != "demo" {
		t.Fatalf("unexpected payload: %+v", payload)
	}

	// File materialized with substituted name; manifest excluded.
	if _, err := os.Stat(filepath.Join(folderPath, "song-x", "song-x.rpp")); err != nil {
		t.Fatalf("seed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(folderPath, "song-x", "template.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest leaked (err=%v)", err)
	}

	// Persisted in the canonical store (workspace.json — project_path has no
	// SQLite column; session reads hydrate it from disk).
	folderWS, err := provider.fileStore.Get(ws.ID)
	if err != nil || folderWS.ProjectPath != "song-x" {
		t.Fatalf("workspace.json project_path = %q err=%v", folderWS.ProjectPath, err)
	}
	if len(folderWS.DirectoryReferences) != 1 {
		t.Fatalf("expected project directory reference, got %#v", folderWS.DirectoryReferences)
	}
	if filepath.Clean(folderWS.DirectoryReferences[0].Path) != filepath.Clean(filepath.Join(folderPath, "song-x")) {
		t.Fatalf("project directory reference path = %q", folderWS.DirectoryReferences[0].Path)
	}
	if folderWS.SharedData["primary_directory_id"] != folderWS.DirectoryReferences[0].ID {
		t.Fatalf("primary directory = %v, want %q", folderWS.SharedData["primary_directory_id"], folderWS.DirectoryReferences[0].ID)
	}
	if folderWS.SharedData["project_directory_id"] != folderWS.DirectoryReferences[0].ID {
		t.Fatalf("project directory = %v, want %q", folderWS.SharedData["project_directory_id"], folderWS.DirectoryReferences[0].ID)
	}

	select {
	case event := <-events:
		if event.Data["project_path"] != "song-x" {
			t.Fatalf("unexpected event payload: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project.created not published")
	}

	// Second create must refuse. The session row has no project_path column,
	// so this also proves the guard falls back to workspace.json.
	if _, err := provider.createProjectTool().Call(ctx, `{"template_id":"demo","name":"Another"}`); err == nil {
		t.Fatal("expected refusal when project already exists")
	} else if !strings.Contains(err.Error(), "already has a project") {
		t.Fatalf("unexpected refusal message: %v", err)
	}
}

func TestWorkspaceCreateProjectToolRefusals(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown template lists available", func(t *testing.T) {
		provider, _, _, cleanup := setupProjectToolProvider(t, &session.Workspace{ID: "ws-unknown", Name: "U"})
		defer cleanup()
		_, err := provider.createProjectTool().Call(ctx, `{"template_id":"nope"}`)
		if err == nil || !strings.Contains(err.Error(), "available templates: demo") {
			t.Fatalf("expected available-template hint, got %v", err)
		}
	})

	t.Run("group workspace", func(t *testing.T) {
		provider, _, _, cleanup := setupProjectToolProvider(t, &session.Workspace{ID: "ws-group", Name: "G", Kind: session.WorkspaceKindGroup})
		defer cleanup()
		_, err := provider.createProjectTool().Call(ctx, `{"template_id":"demo"}`)
		if err == nil || !strings.Contains(err.Error(), "group workspaces cannot have a project") {
			t.Fatalf("expected group refusal, got %v", err)
		}
	})

	t.Run("custom name used for slug", func(t *testing.T) {
		provider, folderPath, _, cleanup := setupProjectToolProvider(t, &session.Workspace{ID: "ws-named", Name: "Workspace Name"})
		defer cleanup()
		result, err := provider.createProjectTool().Call(ctx, `{"template_id":"demo","name":"Midnight Run"}`)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if !strings.Contains(result, `"midnight-run"`) {
			t.Fatalf("expected midnight-run in result: %s", result)
		}
		if _, err := os.Stat(filepath.Join(folderPath, "midnight-run", "midnight-run.rpp")); err != nil {
			t.Fatalf("named project missing: %v", err)
		}
	})
}
