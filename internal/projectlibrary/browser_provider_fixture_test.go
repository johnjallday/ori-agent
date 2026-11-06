package projectlibrary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// An inert test-only contribution installed by the ordinary host review/install
// API in a verified disposable sandbox. No MCP, executable, agent or model.
const browserProviderContribution = `{
 "schema_version":1,"name":"browser-fixture","version":"1.0.0",
 "protocol":{"min":1,"max":1},"requires_host_features":["independent_program_homes_v1"],
 "capabilities":[],"services":[],"blueprints":[],
 "assistant_program_homes":[{
   "schema_version":1,"version":1,"id":"browser-guide","station_name":"Project Browser Fixture",
   "default_primary_name":"Guide","hire_title":"Set up guide",
   "roles":[{"id":"guide","label":"Guide","required":true,"primary":true,"role":"orchestrator","system_prompt":"Fixture only; never execute."}],
   "stages":[{"id":"helper","label":"Helper","accepted_completion_threshold":0}],
   "reflection":{"minimum_projects":3,"cadence_hours":168,"max_projects":16,"max_events_per_project":16,"max_candidates":8,"max_evidence":8,"rubric":"Fixture metadata only; never execute."}
 }]
}`

func writeBrowserProviderFixture(t *testing.T, root string) string {
	t.Helper()
	destination := filepath.Join(root, "provider")
	for _, manifest := range []struct{ dir, body string }{
		{".claude-plugin", `{"name":"browser-fixture","version":"1.0.0","description":"Inert disposable browser acceptance fixture"}`},
		{plugin.OriManifestDir, browserProviderContribution},
	} {
		dir := filepath.Join(destination, manifest.dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

func TestBrowserFixture_GenericProviderIsInert(t *testing.T) {
	path := writeBrowserProviderFixture(t, t.TempDir())
	descriptor, err := plugin.Load(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Name != "browser-fixture" || len(descriptor.MCPServers) != 0 || len(descriptor.Skills) != 0 || len(descriptor.WorkspaceSurfaces.AssistantProgramHomes) != 1 {
		t.Fatalf("unexpected executable fixture: %+v", descriptor)
	}
}

// This binds only the not-yet-imported portable fixture to the REAL installed
// host receipt. It never writes the plugin registry or the running host DB.
func TestBrowserFixtureBindProvider(t *testing.T) {
	if os.Getenv("ORI_LIBRARY_BROWSER_BIND_PROVIDER") != "1" {
		t.Skip("opt-in disposable fixture preparation, not a live integration pass")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := filepath.EvalSymlinks(filepath.Join(cwd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := browserFixtureSandbox(os.Getenv("ORI_LIBRARY_BROWSER_SANDBOX"), owner)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(sandbox, "project-library-browser-fixture")
	plugins, err := plugin.NewStore(filepath.Join(sandbox, "plugins")).List()
	if err != nil {
		t.Fatal(err)
	}
	var installed *plugin.InstalledPlugin
	for i := range plugins {
		if plugins[i].Name == "browser-fixture" {
			installed = &plugins[i]
		}
	}
	if installed == nil || !installed.Enabled || installed.InstallDir != filepath.Join(root, "provider") || installed.WorkspaceSurfaces == nil || len(installed.WorkspaceSurfaces.AssistantProgramHomes) != 1 {
		t.Fatal("the exact inert fixture was not installed by the host")
	}
	declaration := installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	pin := &workspace.AssistantProgramHomeOwner{PluginID: installed.Name, PluginVersion: installed.Version,
		ProgramID: declaration.ID, HomeSchemaVersion: declaration.SchemaVersion, HomeVersion: declaration.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration), PluginGeneration: installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint}
	if !plugin.IndependentHomeProviderEvidenceAvailable(plugins, pin) {
		t.Fatal("real installed provider receipt did not validate")
	}
	folders, err := workspace.NewFileStore(filepath.Join(root, "exports"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = folders.Close() }()
	if err := folders.Update("browser-home", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		if state == nil || state.HomeProvider != nil || state.Key.PluginID != installed.Name {
			t.Fatal("refusing to replace an existing provider pin")
		}
		home.CreatedAt = time.Now().UTC() // Fixture is imported only after this receipt was created.
		state.HomeProvider, state.Declaration = pin, declaration.AssistantProgram()
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A sanitized receipt for evidence, not permission to install elsewhere.
	receipt, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "provider-receipt.json"), receipt, 0o600); err != nil {
		t.Fatal(err)
	}
}
