package sessionhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// writeCapabilityTemplate adds a blueprint declaring built-in capability
// installs to the library the handler reads.
func writeCapabilityTemplate(t *testing.T, libDir, id, manifest string) {
	t.Helper()
	dir := filepath.Join(libDir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

// capabilityTemplateEnv is templateTestEnv plus a library the test controls.
func capabilityTemplateEnv(t *testing.T) (*Handler, string, func()) {
	t.Helper()
	handler, cleanup := createTestHandler(t)

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		cleanup()
		t.Fatalf("NewFileStore: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)
	handler.SetWorkspaceTaskStore(agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(handler.store), fileStore))

	libDir := t.TempDir()
	handler.SetTemplatesRootResolver(func() string { return libDir })
	return handler, libDir, cleanup
}

// TestCreateWorkspace_PersistsABlueprintDeclaredCapability is FR-32: a
// workspace created from a blueprint that declares File Janitor has the
// capability recorded, not merely implied by its template ID.
func TestCreateWorkspace_PersistsABlueprintDeclaredCapability(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()

	writeCapabilityTemplate(t, libDir, "janitor-blueprint", `{
		"name": "File Janitor",
		"builtin": true,
		"capabilities": [{"id": "file-janitor", "source": "downloads-janitor-preset"}]
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Tidy","template_id":"janitor-blueprint"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	folder := resp["folder"].(map[string]any)
	workspaceID := folder["id"].(string)

	ws, err := handler.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	record, ok := ws.GetInstalledCapability(agentworkspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("the blueprint's capability was not installed: %+v", ws.GetInstalledCapabilities())
	}
	if record.Source != "downloads-janitor-preset" {
		t.Fatalf("install source = %q, want the preset's", record.Source)
	}
	if record.Version != workspacecapability.FileJanitorDefinitionVersion {
		t.Fatalf("version = %d, want this build's definition version", record.Version)
	}
	if record.InstalledAt.IsZero() {
		t.Fatal("install timestamp was not stamped")
	}

	// Provenance survived the same update that wrote the install: a workspace
	// must never be recorded as coming from this blueprint while lacking the
	// capability the blueprint exists to install.
	if !ws.IsFromTemplate("janitor-blueprint") {
		t.Fatalf("template provenance was lost: %+v", ws.GetTemplateProvenance())
	}
}

// TestCreateWorkspace_WithoutADeclarationInstallsNothing is the negative
// control for FR-136: creating from an ordinary blueprint must not produce a
// capability record, however Janitor-ish its name.
func TestCreateWorkspace_WithoutADeclarationInstallsNothing(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()

	writeCapabilityTemplate(t, libDir, "plain-blueprint", `{
		"name": "Downloads Janitor Helper",
		"builtin": true
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Downloads Janitor","template_id":"plain-blueprint"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	workspaceID := resp["folder"].(map[string]any)["id"].(string)

	ws, err := handler.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if caps := ws.GetInstalledCapabilities(); len(caps) != 0 {
		t.Fatalf("a blueprint declaring nothing installed %+v", caps)
	}
}

// TestCreateWorkspace_IgnoresAnUnknownDeclaredCapability proves the fail-closed
// path survives all the way to creation: a manifest naming a capability this
// build does not provide yields a workspace with no install record, and no
// error that would block creation.
func TestCreateWorkspace_IgnoresAnUnknownDeclaredCapability(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()

	writeCapabilityTemplate(t, libDir, "future-blueprint", `{
		"name": "From The Future",
		"builtin": true,
		"capabilities": [{"id": "capability-from-the-future"}]
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Future","template_id":"future-blueprint"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("an unknown capability must not block creation: %d %s", w.Code, w.Body.String())
	}
	workspaceID := resp["folder"].(map[string]any)["id"].(string)

	ws, err := handler.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if caps := ws.GetInstalledCapabilities(); len(caps) != 0 {
		t.Fatalf("an uncompiled capability was installed: %+v", caps)
	}
}

// TestCreateWorkspace_DeclaredCapabilityGrantsNothing is FR-20 at creation: the
// blueprint records the install, and nothing else. No folder is granted, no
// watcher registered, no schedule enabled — those wait for the user's approval
// in setup.
func TestCreateWorkspace_DeclaredCapabilityGrantsNothing(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()

	writeCapabilityTemplate(t, libDir, "janitor-blueprint", `{
		"name": "File Janitor",
		"builtin": true,
		"capabilities": [{"id": "file-janitor"}],
		"directory_requirements": [
			{"key": "file-janitor-root", "label": "Folder to tidy", "suggested_path": "~/Downloads"}
		]
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Tidy","template_id":"janitor-blueprint"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	workspaceID := resp["folder"].(map[string]any)["id"].(string)

	ws, err := handler.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ws.HasInstalledCapability(agentworkspace.CapabilityFileJanitor) {
		t.Fatal("the capability should be installed")
	}

	// The declared folder is still a request, not a grant.
	pending := ws.PendingDirectoryRequirements()
	if len(pending) != 1 {
		t.Fatalf("expected the folder to remain an unresolved requirement: %+v", pending)
	}
	if got := pending[0].SuggestedPath; got != "~/Downloads" {
		t.Fatalf("the suggestion was resolved at creation: %q", got)
	}

	// And the capability owns nothing yet, because setup has not run.
	record, _ := ws.GetInstalledCapability(agentworkspace.CapabilityFileJanitor)
	if len(record.OwnedResources) != 0 {
		t.Fatalf("creation recorded owned resources before any grant: %+v", record.OwnedResources)
	}
}
