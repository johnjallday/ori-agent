package sessionhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// folderWorkspace reads the canonical workspace.json record. Template
// provenance has no SQLite column, so the SyncStore's primary Get returns it as
// nil; the folder store is the source of truth for it.
func folderWorkspace(t *testing.T, handler *Handler, wsID string) *agentworkspace.Workspace {
	t.Helper()
	sync, ok := handler.workspaceTaskStore.(*agentworkspace.SyncStore)
	if !ok {
		t.Fatalf("expected a SyncStore-backed workspace task store, got %T", handler.workspaceTaskStore)
	}
	ws, err := sync.GetFolderWorkspace(wsID)
	if err != nil {
		t.Fatalf("GetFolderWorkspace: %v", err)
	}
	return ws
}

// writeSetupRequirementTemplate adds a built-in library template that asks for
// one local folder and wants a watcher plus a daily catch-up afterwards —
// Downloads Janitor's shape, without depending on that template existing yet.
func writeSetupRequirementTemplate(t *testing.T, libDir string) {
	t.Helper()
	dir := filepath.Join(libDir, "folder-template")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "Folder Template",
		"builtin": true,
		"builtin_version": 1,
		"directory_requirements": [
			{"key": "inbox-root", "label": "Inbox folder", "suggested_path": "~/Downloads", "access_disclosure": "Ori can list files here."}
		],
		"automation_recipes": [
			{
				"directory_key": "inbox-root",
				"watch": {"events": ["create", "rename"], "debounce_seconds": 300, "exclude_subdirectories": ["Filed"]},
				"daily_scan": {"local_time": "09:00"}
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWorkspace_CarriesSetupRequirementsUnresolved(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeSetupRequirementTemplate(t, handler.templatesRootResolver())

	w, resp := postCreateWorkspace(t, handler, `{"name":"Inbox WS","template_id":"folder-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in response: %s", w.Body.String())
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}

	ws := folderWorkspace(t, handler, wsID)

	reqs := ws.PendingDirectoryRequirements()
	if len(reqs) != 1 || reqs[0].Key != "inbox-root" || reqs[0].Label != "Inbox folder" {
		t.Fatalf("directory requirement not carried into the workspace: %+v", reqs)
	}
	// Unresolved: creation records the suggestion verbatim. Expanding "~" or
	// picking a path here would grant folder access the user never confirmed.
	if reqs[0].SuggestedPath != "~/Downloads" {
		t.Fatalf("suggested path was resolved during creation: %q", reqs[0].SuggestedPath)
	}
	if reqs[0].AccessDisclosure == "" {
		t.Fatal("access disclosure must be carried so setup can show it before approval")
	}

	recipe, ok := ws.TemplateAutomationRecipeFor("inbox-root")
	if !ok {
		t.Fatal("automation recipe not carried into the workspace")
	}
	if recipe.Watch == nil || recipe.Watch.DebounceSeconds != 300 || len(recipe.Watch.Events) != 2 {
		t.Fatalf("watch recipe not carried intact: %+v", recipe.Watch)
	}
	if recipe.DailyScan == nil || recipe.DailyScan.LocalTime != "09:00" {
		t.Fatalf("daily scan recipe not carried intact: %+v", recipe.DailyScan)
	}

	// The recipe is a request, not a registration: creation must not have
	// selected a folder or granted access to one.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	suggested := filepath.Join(home, "Downloads")
	for _, ref := range ws.DirectoryReferences {
		if filepath.Clean(ref.Path) == suggested {
			t.Fatalf("creation linked the suggested folder before the user confirmed it: %+v", ref)
		}
	}
}

func TestCreateWorkspace_TemplateWithoutSetupRequirementsCarriesNone(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Plain WS","template_id":"demo-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}

	ws := folderWorkspace(t, handler, wsID)
	if len(ws.PendingDirectoryRequirements()) != 0 {
		t.Fatalf("expected no directory requirements, got %+v", ws.PendingDirectoryRequirements())
	}
	if _, ok := ws.TemplateAutomationRecipeFor("inbox-root"); ok {
		t.Fatal("expected no automation recipe for a template that declares none")
	}
}
