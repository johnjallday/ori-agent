package downloadsjanitor

import (
	"strings"
	"testing"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// This feature added fields to shared types and a new action kind to a shared
// trigger vocabulary. These tests assert the things it must NOT have changed.
//
// The risk is not that Downloads Janitor breaks — its own tests cover that. It
// is that a workspace with nothing to do with Downloads behaves differently
// because this feature exists.

// A workspace that is not a Janitor workspace gets no Janitor surface, no
// directory reference, no binding, and no automation.
func TestRegression_UnrelatedWorkspacesAreUntouched(t *testing.T) {
	service, workspaces := newTestService(t)
	plain := workspaces.workspaces["ws-2"]

	if service.AppliesTo("ws-2") {
		t.Fatal("a plain workspace must not mount the Janitor surface")
	}
	status, err := service.Status("ws-2")
	if err != nil {
		t.Fatalf("status for a plain workspace should still answer: %v", err)
	}
	if status.Applies {
		t.Fatal("status must report the feature does not apply")
	}
	if len(plain.DirectoryReferences) != 0 || len(plain.MCPBindings) != 0 {
		t.Fatalf("a plain workspace must be untouched: %+v / %+v", plain.DirectoryReferences, plain.MCPBindings)
	}
}

// A workspace with an existing filesystem binding keeps it, with its own tools,
// when a Janitor folder is added alongside.
func TestRegression_ExistingFilesystemBindingsKeepTheirTools(t *testing.T) {
	service, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]

	// An ordinary workspace-files binding, as workspace creation makes it:
	// no allowlist, meaning every tool.
	existing := workspace.MCPBinding{
		ID:         "existing-binding",
		ServerName: "filesystem",
		Alias:      "workspace_files",
		Enabled:    true,
		Config:     map[string]any{"roots": []string{t.TempDir()}},
	}
	if err := ws.UpsertMCPBinding(existing); err != nil {
		t.Fatal(err)
	}

	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	var found bool
	for _, binding := range ws.MCPBindings {
		if binding.ID != "existing-binding" {
			continue
		}
		found = true
		// Untouched: same roots, same (absent) restriction.
		if !binding.AllowsAllTools() {
			t.Fatal("the existing binding's tool set must not be narrowed by the Janitor")
		}
		roots := toStringSlice(binding.Config["roots"])
		for _, r := range roots {
			if r == root {
				t.Fatal("the existing binding must not gain the Downloads root")
			}
		}
	}
	if !found {
		t.Fatal("the existing binding must survive setup")
	}
}

// Adding the Janitor's directory reference leaves other references alone.
func TestRegression_ExistingDirectoryReferencesSurvive(t *testing.T) {
	service, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]
	existing := t.TempDir()
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Project", Path: existing}); err != nil {
		t.Fatal(err)
	}
	originalID := ws.DirectoryReferences[0].ID

	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}

	if len(ws.DirectoryReferences) != 2 {
		t.Fatalf("expected the original plus the Janitor's: %+v", ws.DirectoryReferences)
	}
	if ws.DirectoryReferences[0].ID != originalID || ws.DirectoryReferences[0].Path != existing {
		t.Fatalf("the existing reference changed: %+v", ws.DirectoryReferences[0])
	}

	// And revoking removes only the Janitor's.
	if _, err := service.RevokeAccess(&recordingLifecycle{}, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}
	if len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].ID != originalID {
		t.Fatalf("revoke must remove only the Janitor's reference: %+v", ws.DirectoryReferences)
	}
}

// Template provenance without Janitor fields still loads, and a template that
// declares no directory requirement carries none.
func TestRegression_TemplatesWithoutJanitorFieldsStillWork(t *testing.T) {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Legacy"})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "writing-project", TemplateName: "Writing Project", Builtin: true, Version: 3,
	})

	provenance := ws.GetTemplateProvenance()
	if provenance.TemplateID != "writing-project" || provenance.Version != 3 {
		t.Fatalf("existing provenance must be unaffected: %+v", provenance)
	}
	if len(provenance.DirectoryRequirements) != 0 || len(provenance.AutomationRecipes) != 0 {
		t.Fatalf("a template that declares nothing must carry nothing: %+v", provenance)
	}
	if ws.PendingDirectoryRequirements() != nil {
		t.Fatal("no requirements means nil, not an empty surface")
	}
	if _, ok := ws.TemplateAutomationRecipeFor("downloads-root"); ok {
		t.Fatal("no recipe means none")
	}
}

// The settings record for a workspace that predates this feature loads as
// unconfigured rather than failing.
func TestRegression_OlderStateLoadsAsUnconfigured(t *testing.T) {
	store, _ := newTestStore(t)
	settings, err := store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("a workspace with no Janitor state must load: %v", err)
	}
	if settings.IsSetUp() {
		t.Fatal("it must load as unconfigured")
	}
	state, err := store.LoadScanState("ws-1")
	if err != nil {
		t.Fatalf("scan state must load: %v", err)
	}
	if len(state.Batches) != 0 || len(state.Actions) != 0 {
		t.Fatalf("no prior work means none: %+v", state)
	}
}

// The Janitor's own category vocabulary is closed and stable: a stored
// candidate from an earlier version still resolves.
func TestRegression_StoredCategoriesStillResolve(t *testing.T) {
	for _, id := range []string{"documents", "images", "audio", "video", "archives", "installers", "data", "other"} {
		if _, err := LookupCategory(id); err != nil {
			t.Fatalf("category %q must remain valid: %v", id, err)
		}
	}
	// And the folder names are stable, since they are on disk in users' folders.
	expected := map[Category]string{
		CategoryDocuments: "Documents", CategoryImages: "Images", CategoryAudio: "Audio",
		CategoryVideo: "Video", CategoryArchives: "Archives", CategoryInstallers: "Installers",
		CategoryData: "Data", CategoryOther: "Other",
	}
	for _, definition := range CategoryRegistry {
		if expected[definition.ID] != definition.FolderName {
			t.Fatalf("category %q folder name changed to %q; existing users have folders on disk",
				definition.ID, definition.FolderName)
		}
	}
}

// The Janitor binding alias is stable: setup finds and repairs its own binding
// by that alias, so changing it would orphan every existing workspace's.
func TestRegression_BindingAliasIsStable(t *testing.T) {
	if JanitorBindingAlias != "downloads_janitor_root" {
		t.Fatalf("the binding alias changed to %q; existing workspaces would keep an orphaned binding", JanitorBindingAlias)
	}
	if DomainKey != "downloads_janitor" {
		t.Fatalf("the domain key changed to %q; existing watch triggers would stop routing", DomainKey)
	}
	if WatchTriggerName == "" || !strings.Contains(WatchTriggerName, "Downloads Janitor") {
		t.Fatalf("the watcher name must stay recognizable: %q", WatchTriggerName)
	}
}
