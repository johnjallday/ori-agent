package sessionhttp

// The blueprint readiness matrix at the creation gate.
//
// internal/server's matrix covers what the catalog projects. This one covers
// what the server — the final authority — does when the user presses Create,
// including the states the catalog cannot have known about because they
// changed after it was drawn.
//
// Each row also states what creation did BEFORE this feature, so the reason a
// state is refused (or still allowed) stays legible.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// matrixPluginLister is the installed-plugin store as the create gate sees it:
// a list of records, or a transient failure.
type matrixPluginLister struct {
	installed []plugin.InstalledPlugin
	err       error
}

func (l matrixPluginLister) List() ([]plugin.InstalledPlugin, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.installed, nil
}

func writeCreateMatrixTemplate(t *testing.T, libDir, id, manifest string) {
	t.Helper()
	dir := filepath.Join(libDir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projecttemplates.ManifestFileName), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o640); err != nil {
		t.Fatal(err)
	}
}

// countWorkspaceState returns the number of workspace folders on disk and
// workspace records in the store, so a refusal can be proven to mutate neither.
func countWorkspaceState(t *testing.T, handler *Handler, baseDir string) (int, int) {
	t.Helper()
	folders, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(folders), len(records)
}

// installedPluginRecord builds a minimal installed-plugin record. Always
// named "owner-plugin" in this file's fixtures — the blueprint each test
// wires up always declares that same dependency name.
func installedPluginRecord(enabled bool) plugin.InstalledPlugin {
	const name = "owner-plugin"
	return plugin.InstalledPlugin{
		Name: name, Version: "1.0.0", Enabled: enabled, Generation: 3,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: name, Version: "1.0.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedArtifacts: []plugin.ResolvedArtifact{{ServiceID: "svc", Available: true}},
	}
}

// conflictReadiness decodes the readiness projection from a refusal body.
func conflictReadiness(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	readiness, ok := resp["readiness"].(map[string]any)
	if !ok {
		t.Fatalf("refusal carries no readiness projection: %v", resp)
	}
	return readiness
}

func assertConflictProjection(t *testing.T, resp map[string]any, state blueprintreadiness.State, ownership blueprintreadiness.Ownership, reason blueprintreadiness.Reason) {
	t.Helper()
	readiness := conflictReadiness(t, resp)
	got := [3]string{
		readiness["state"].(string),
		readiness["ownership"].(string),
		readiness["reason"].(string),
	}
	want := [3]string{string(state), string(ownership), string(reason)}
	if got != want {
		t.Fatalf("conflict readiness = %v, want %v (body %v)", got, want, resp)
	}
	actions, _ := readiness["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("a refusal offered no way forward: %v", readiness)
	}
	for _, raw := range actions {
		name, _ := raw.(string)
		if _, ok := blueprintreadiness.ParseAction(name); !ok {
			t.Fatalf("refusal offered an action outside the allowlist: %q", name)
		}
	}
}

// TestBlueprintCreateGateMatrix_TemplateOwnedStates covers the rows the
// template library alone decides.
func TestBlueprintCreateGateMatrix_TemplateOwnedStates(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()

	writeCreateMatrixTemplate(t, libDir, "research-project", `{
		"name":"Research Project","builtin":true,"builtin_version":1,
		"agents":[{"name":"Researcher"}]
	}`)
	writeCreateMatrixTemplate(t, libDir, "my-notes", `{
		"name":"My Notes","agents":[{"name":"Notes Lead"}]
	}`)
	writeCreateMatrixTemplate(t, libDir, "broken-user-template", `{
		"name":"Broken User Template","agents":[{"name":"Lead"}],
		"setup_wizard":{"version":1,"title":"Set up","steps":[
			{"id":"readiness","kind":"readiness","adapter":"not_a_real_adapter","required":true}
		]}
	}`)
	// Retired: claims built-in ownership under an ID the app no longer ships.
	writeCreateMatrixTemplate(t, libDir, "song-production", `{
		"name":"Song Production","builtin":true,"builtin_version":3,
		"agents":[{"name":"Producer"}]
	}`)
	writeCreateMatrixTemplate(t, libDir, "unknown-runtime", `{
		"name":"Unknown Runtime","agents":[{"name":"Lead"}],
		"runtime_requirements":{"schema_version":1,
			"operating_modes":[
				{"id":"limited","label":"Limited","description":"Use files."},
				{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["runtime"]}
			],
			"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"absent_runtime"}]}
	}`)

	t.Run("valid builtin creates", func(t *testing.T) {
		w, _ := postCreateWorkspace(t, handler, `{"name":"Research WS","template_id":"research-project"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("valid user template creates", func(t *testing.T) {
		w, _ := postCreateWorkspace(t, handler, `{"name":"Notes WS","template_id":"my-notes"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid author-owned manifest keeps its author guidance", func(t *testing.T) {
		beforeFolders, beforeRecords := countWorkspaceState(t, handler, baseDir)
		w, resp := postCreateWorkspace(t, handler, `{"name":"Broken WS","template_id":"broken-user-template"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonManifestInvalid)
		message, _ := resp["error"].(string)
		// Correct for this row and only this row: the file really is the
		// user's to fix, so the long-standing wording is preserved.
		if !strings.Contains(message, "template.json") || !strings.Contains(message, "not_a_real_adapter") {
			t.Fatalf("author guidance missing from refusal: %q", message)
		}
		if diagnostic, _ := resp["setup_wizard_error"].(string); diagnostic == "" {
			t.Fatal("the pre-existing structured diagnostic was dropped")
		}
		if afterFolders, afterRecords := countWorkspaceState(t, handler, baseDir); afterFolders != beforeFolders || afterRecords != beforeRecords {
			t.Fatalf("refusal mutated workspace state: folders %d->%d records %d->%d",
				beforeFolders, afterFolders, beforeRecords, afterRecords)
		}
	})

	t.Run("retired on-disk builtin is refused without blaming the user or touching its files", func(t *testing.T) {
		// Before: it created workspaces like any shipped blueprint, because
		// its own `builtin: true` was trusted verbatim.
		if projecttemplates.IsBuiltinStarterID("song-production") {
			t.Skip("embedded starter catalog now ships this ID")
		}
		beforeFolders, beforeRecords := countWorkspaceState(t, handler, baseDir)
		w, resp := postCreateWorkspace(t, handler, `{"name":"Song WS","template_id":"song-production"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonBlueprintRetired)
		if message, _ := resp["error"].(string); strings.Contains(message, "template.json") {
			t.Fatalf("a retired built-in told the user to edit shipped JSON: %q", message)
		}
		// The contract is refusal, never deletion.
		for _, name := range []string{"template.json", "seed.txt"} {
			if _, err := os.Stat(filepath.Join(libDir, "song-production", name)); err != nil {
				t.Fatalf("a refused retired blueprint lost %s: %v", name, err)
			}
		}
		if afterFolders, afterRecords := countWorkspaceState(t, handler, baseDir); afterFolders != beforeFolders || afterRecords != beforeRecords {
			t.Fatalf("refusal mutated workspace state: folders %d->%d records %d->%d",
				beforeFolders, afterFolders, beforeRecords, afterRecords)
		}
	})

	t.Run("unavailable runtime provider is refused with the author's diagnostic", func(t *testing.T) {
		beforeFolders, beforeRecords := countWorkspaceState(t, handler, baseDir)
		w, resp := postCreateWorkspace(t, handler, `{"name":"Runtime WS","template_id":"unknown-runtime"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonManifestInvalid)
		if diagnostic, _ := resp["runtime_requirements_error"].(string); !strings.Contains(diagnostic, "absent_runtime") {
			t.Fatalf("runtime diagnostic does not name the adapter: %q", diagnostic)
		}
		if afterFolders, afterRecords := countWorkspaceState(t, handler, baseDir); afterFolders != beforeFolders || afterRecords != beforeRecords {
			t.Fatalf("refusal mutated workspace state: folders %d->%d records %d->%d",
				beforeFolders, afterFolders, beforeRecords, afterRecords)
		}
	})
}

// TestBlueprintCreateGateMatrix_PluginDependencyStates covers a blueprint that
// declares a plugin dependency, across every state its plugin can be in.
func TestBlueprintCreateGateMatrix_PluginDependencyStates(t *testing.T) {
	manifest := `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["owner-plugin"],"plugin_sources":{"owner-plugin":"https://example.test/owner.git"}}
	}`

	newEnv := func(t *testing.T, lister installedPluginLister) (*Handler, string, func()) {
		t.Helper()
		handler, baseDir, _, cleanup := templateTestEnv(t)
		writeCreateMatrixTemplate(t, handler.templatesRootResolver(), "needs-plugin", manifest)
		handler.SetInstalledPluginLister(lister)
		return handler, baseDir, cleanup
	}

	t.Run("missing plugin is refused with an install action and the legacy keys", func(t *testing.T) {
		handler, baseDir, cleanup := newEnv(t, matrixPluginLister{})
		defer cleanup()
		beforeFolders, beforeRecords := countWorkspaceState(t, handler, baseDir)

		w, resp := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonPluginInstallRequired)
		missing, _ := resp["missing_plugins"].([]any)
		if len(missing) != 1 || missing[0] != "owner-plugin" {
			t.Fatalf("the pre-existing missing_plugins key changed shape: %v", resp)
		}
		// The refusal must not hand the browser a source to install from; the
		// trust preview is where a source is disclosed.
		if strings.Contains(w.Body.String(), "example.test") {
			t.Fatalf("the conflict disclosed an untrusted source: %s", w.Body.String())
		}
		if afterFolders, afterRecords := countWorkspaceState(t, handler, baseDir); afterFolders != beforeFolders || afterRecords != beforeRecords {
			t.Fatalf("refusal mutated workspace state: folders %d->%d records %d->%d",
				beforeFolders, afterFolders, beforeRecords, afterRecords)
		}
	})

	t.Run("installed but disabled asks to enable, not to install", func(t *testing.T) {
		handler, _, cleanup := newEnv(t, matrixPluginLister{
			installed: []plugin.InstalledPlugin{installedPluginRecord(false)},
		})
		defer cleanup()

		w, resp := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonPluginEnableRequired)
		disabled, _ := resp["disabled_plugins"].([]any)
		if len(disabled) != 1 || disabled[0] != "owner-plugin" {
			t.Fatalf("the pre-existing disabled_plugins key changed shape: %v", resp)
		}
		if missing, _ := resp["missing_plugins"].([]any); len(missing) != 0 {
			t.Fatalf("a disabled plugin was reported as missing: %v", resp)
		}
	})

	t.Run("enabled plugin creates", func(t *testing.T) {
		handler, _, cleanup := newEnv(t, matrixPluginLister{
			installed: []plugin.InstalledPlugin{installedPluginRecord(true)},
		})
		defer cleanup()

		w, _ := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("an unsupported platform is refused without offering a pointless retry", func(t *testing.T) {
		unsupported := installedPluginRecord(true)
		unsupported.ResolvedArtifacts = []plugin.ResolvedArtifact{
			{ServiceID: "svc", Available: false, Unavailable: "platform_unsupported"},
		}
		handler, _, cleanup := newEnv(t, matrixPluginLister{installed: []plugin.InstalledPlugin{unsupported}})
		defer cleanup()

		w, resp := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPlatformUnsupported)
	})

	t.Run("an incompatible protocol is refused after a host upgrade", func(t *testing.T) {
		incompatible := installedPluginRecord(true)
		incompatible.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{
			Min: plugin.SurfaceProtocolVersion + 1, Max: plugin.SurfaceProtocolVersion + 2,
		}
		handler, _, cleanup := newEnv(t, matrixPluginLister{installed: []plugin.InstalledPlugin{incompatible}})
		defer cleanup()

		w, resp := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonProtocolIncompatible)
	})

	t.Run("a transient plugin-list failure refuses instead of failing open", func(t *testing.T) {
		// Before: the error was swallowed, nothing was reported unsatisfied,
		// and the workspace was created with its required plugin absent.
		handler, baseDir, cleanup := newEnv(t, matrixPluginLister{err: errors.New("plugin store unavailable")})
		defer cleanup()
		beforeFolders, beforeRecords := countWorkspaceState(t, handler, baseDir)

		w, resp := postCreateWorkspace(t, handler, `{"name":"Plugin WS","template_id":"needs-plugin"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonDependencyStateUnknown)
		if afterFolders, afterRecords := countWorkspaceState(t, handler, baseDir); afterFolders != beforeFolders || afterRecords != beforeRecords {
			t.Fatalf("refusal mutated workspace state: folders %d->%d records %d->%d",
				beforeFolders, afterFolders, beforeRecords, afterRecords)
		}
	})

	t.Run("a setup wizard still exempts a blueprint from the plugin lifecycle gate", func(t *testing.T) {
		// Deliberately preserved: the wizard may offer an operating mode that
		// does not need the declared plugin, so the wizard decides, not this
		// gate. The exemption covers plugin lifecycle states only.
		handler, _, cleanup := newEnv(t, matrixPluginLister{})
		defer cleanup()
		writeCreateMatrixTemplate(t, handler.templatesRootResolver(), "wizard-needs-plugin", `{
			"name":"Wizard Needs Plugin","agents":[{"name":"Lead"}],
			"tools":{"plugins":["owner-plugin"],"plugin_sources":{"owner-plugin":"https://example.test/owner.git"}},
			"setup_wizard":{"version":1,"title":"Set up","steps":[
				{"id":"plugin","kind":"plugin_readiness","requirement_key":"owner-plugin","adapter":"calendar_ops","required":true},
				{"id":"summary","kind":"summary","required":false}
			]}
		}`)

		w, _ := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-needs-plugin"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("a setup wizard does not exempt a hard blocker", func(t *testing.T) {
		unsupported := installedPluginRecord(true)
		unsupported.ResolvedArtifacts = []plugin.ResolvedArtifact{
			{ServiceID: "svc", Available: false, Unavailable: "platform_unsupported"},
		}
		handler, _, cleanup := newEnv(t, matrixPluginLister{installed: []plugin.InstalledPlugin{unsupported}})
		defer cleanup()
		writeCreateMatrixTemplate(t, handler.templatesRootResolver(), "wizard-unsupported", `{
			"name":"Wizard Unsupported","agents":[{"name":"Lead"}],
			"tools":{"plugins":["owner-plugin"]},
			"setup_wizard":{"version":1,"title":"Set up","steps":[
				{"id":"plugin","kind":"plugin_readiness","requirement_key":"owner-plugin","adapter":"calendar_ops","required":true},
				{"id":"summary","kind":"summary","required":false}
			]}
		}`)

		w, resp := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-unsupported"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("a wizard exempted a plugin that cannot run here: %d %s", w.Code, w.Body.String())
		}
		assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPlatformUnsupported)
	})
}

// TestBlueprintCreateGateMatrix_ConflictsDiscloseNothingSensitive proves the
// refusal body stays inside the public contract whatever went wrong.
func TestBlueprintCreateGateMatrix_ConflictsDiscloseNothingSensitive(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()
	installed := installedPluginRecord(false)
	installed.InstallDir = "/Users/someone/Library/Application Support/ori/plugins/owner-plugin"
	handler.SetInstalledPluginLister(matrixPluginLister{installed: []plugin.InstalledPlugin{installed}})

	writeCreateMatrixTemplate(t, libDir, "hostile", `{
		"name":"Hostile","agents":[{"name":"Lead"}],
		"description":"Run https://evil.test/setup.sh first",
		"tools":{"plugins":["owner-plugin"],"plugin_sources":{"owner-plugin":"https://evil.test/owner.git"}}
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Hostile WS","template_id":"hostile"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	readiness := conflictReadiness(t, resp)
	encoded, _ := readiness["summary"].(string)
	detail, _ := readiness["detail"].(string)
	for _, forbidden := range []string{"://", "/Users/", "Application Support", "evil.test"} {
		if strings.Contains(encoded+detail, forbidden) {
			t.Errorf("readiness copy discloses %q: %q / %q", forbidden, encoded, detail)
		}
	}
	dependency, _ := readiness["dependency"].(map[string]any)
	if dependency == nil {
		t.Fatalf("dependency missing: %v", readiness)
	}
	if _, leaked := dependency["source"]; leaked {
		t.Fatalf("the dependency descriptor carries a source: %v", dependency)
	}
	if declared, _ := dependency["source_declared"].(bool); !declared {
		t.Fatal("a declared source was not reported as available for the trust preview")
	}
}

// TestBlueprintCreateGateBaseline_ManifestDiagnosticNeverLeaksForANonUserOwner
// guards the legacy compatibility keys the create-conflict body still emits
// alongside the readiness projection. The projection itself withholds a
// parser diagnostic from anyone but the manifest's actual author; a
// shipped-built-in row reaching the same failure must not reopen that
// through `setup_wizard_error` / `runtime_requirements_error`.
func TestBlueprintCreateGateMatrix_ManifestDiagnosticNeverLeaksForANonUserOwner(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()

	// A shipped built-in (still in the embedded starter catalog, so it is not
	// classified as retired) whose manifest is nonetheless broken.
	writeCreateMatrixTemplate(t, libDir, "research-project", `{
		"name":"Research Project","builtin":true,"builtin_version":1,
		"agents":[{"name":"Lead"}],
		"setup_wizard":{"version":1,"title":"Set up","steps":[
			{"id":"readiness","kind":"readiness","adapter":"not_a_real_adapter","required":true}
		]}
	}`)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Research WS","template_id":"research-project"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	assertConflictProjection(t, resp, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonManifestInvalid)

	if _, present := resp["setup_wizard_error"]; present {
		t.Fatalf("a shipped built-in's parser diagnostic leaked through the legacy key: %v", resp)
	}
	if _, present := resp["runtime_requirements_error"]; present {
		t.Fatalf("a shipped built-in's parser diagnostic leaked through the legacy key: %v", resp)
	}
	message, _ := resp["error"].(string)
	if strings.Contains(message, "not_a_real_adapter") || strings.Contains(message, "template.json") {
		t.Fatalf("the human-readable error told the user to edit shipped JSON: %q", message)
	}
	readiness := conflictReadiness(t, resp)
	if diagnostic, _ := readiness["diagnostic"].(string); diagnostic != "" {
		t.Fatalf("the readiness projection itself carried a diagnostic for a non-user owner: %q", diagnostic)
	}
}
