package sessionhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// writeWizardTemplate adds a library template declaring a setup wizard whose
// steps reference its own directory, capability, and plugin declarations.
func writeWizardTemplate(t *testing.T, libDir, title string) {
	t.Helper()
	dir := filepath.Join(libDir, "wizard-template")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "Wizard Template",
		"builtin": true,
		"builtin_version": 1,
		"tools": {"plugins": ["reaper-plugin"], "plugin_sources": {"reaper-plugin": "https://example.test/reaper-plugin.git"}},
		"capability_requirements": [{"key": "calendar", "required_operations": ["list_events"]}],
		"directory_requirements": [{"key": "inbox-root", "label": "Inbox folder", "suggested_path": "~/Downloads"}],
		"automation_recipes": [{"directory_key": "inbox-root", "watch": {"events": ["create"]}, "daily_scan": {"local_time": "09:00"}}],
		"setup_wizard": {
			"version": 1,
			"title": "` + title + `",
			"steps": [
				{"id": "folder", "kind": "directory", "requirement_key": "inbox-root", "required": true},
				{"id": "automation", "kind": "automation_review", "requirement_key": "inbox-root", "required": true},
				{"id": "readiness", "kind": "readiness", "adapter": "downloads_janitor", "required": true},
				{"id": "summary", "kind": "summary", "required": false}
			]
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestCreateWorkspace_SnapshotsSetupWizardIntoProvenance covers FR-16/FR-17:
// creation records the wizard, the requirements its steps reference, and the
// blueprint's identity — and a later blueprint edit does not reach back into an
// existing workspace's setup.
func TestCreateWorkspace_SnapshotsSetupWizardIntoProvenance(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	libDir := handler.templatesRootResolver()
	writeWizardTemplate(t, libDir, "Set up the workspace")

	w, resp := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}

	prov := folderWorkspace(t, handler, wsID).GetTemplateProvenance()
	if prov == nil || prov.SetupWizard == nil {
		t.Fatalf("creation did not snapshot the wizard: %+v", prov)
	}
	if prov.TemplateID != "wizard-template" || prov.Version != 1 {
		t.Fatalf("wizard snapshot is missing its source blueprint identity: %+v", prov)
	}
	if prov.SetupWizard.Title != "Set up the workspace" || len(prov.SetupWizard.Steps) != 4 {
		t.Fatalf("wizard snapshot changed during creation: %+v", prov.SetupWizard)
	}
	// The steps' referenced declarations travel with them, so setup can be
	// rendered and repaired from the workspace alone.
	if len(prov.CapabilityRequirements) != 1 || prov.CapabilityRequirements[0].Key != "calendar" {
		t.Fatalf("capability requirements not snapshotted: %+v", prov.CapabilityRequirements)
	}
	if len(prov.Plugins) != 1 || prov.PluginSources["reaper-plugin"] == "" {
		t.Fatalf("plugin declarations not snapshotted: %v / %v", prov.Plugins, prov.PluginSources)
	}
	if len(prov.DirectoryRequirements) != 1 || len(prov.AutomationRecipes) != 1 {
		t.Fatalf("referenced setup requirements not snapshotted: %+v", prov)
	}

	// The blueprint is edited afterwards — a different wizard, same id.
	writeWizardTemplate(t, libDir, "Completely different setup")
	after := folderWorkspace(t, handler, wsID).GetTemplateProvenance()
	if after.SetupWizard.Title != "Set up the workspace" {
		t.Fatalf("editing the blueprint rewrote an existing workspace's setup: %+v", after.SetupWizard)
	}
}

// TestCreateWorkspace_RefusesTemplateWithUnusableSetupWizard covers FR-12: a
// blueprint whose setup wizard cannot be understood must not produce a
// workspace at all. Creating one anyway is the bad outcome the fail-closed
// contract exists to prevent — the user would get a workspace that silently
// never asks for the folder, account, or permission its author declared.
func TestCreateWorkspace_RefusesTemplateWithUnusableSetupWizard(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
	defer cleanup()

	libDir := handler.templatesRootResolver()
	dir := filepath.Join(libDir, "broken-wizard")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Valid in every other respect; the wizard names an adapter that does not
	// exist.
	manifest := `{
		"name": "Broken Wizard",
		"builtin": true,
		"builtin_version": 1,
		"directory_requirements": [{"key": "inbox-root", "label": "Inbox folder"}],
		"setup_wizard": {
			"version": 1,
			"title": "Set up Broken",
			"steps": [{"id": "readiness", "kind": "readiness", "adapter": "not_a_real_adapter", "required": true}]
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatal(err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Broken WS","template_id":"broken-wizard"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	message, _ := resp["error"].(string)
	if !strings.Contains(message, "setup wizard") || !strings.Contains(message, "not_a_real_adapter") {
		t.Fatalf("error must name the problem actionably, got %q", message)
	}
	if diag, _ := resp["setup_wizard_error"].(string); !strings.Contains(diag, "unregistered adapter") {
		t.Fatalf("expected a machine-readable diagnostic, got %q", diag)
	}

	after, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a refused creation left %d workspace folders behind (was %d)", len(after), len(before))
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
