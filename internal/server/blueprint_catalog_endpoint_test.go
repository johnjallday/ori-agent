package server

// The readiness contract as it leaves the process.
//
// The matrix tests exercise the projection; this one drives the real
// GET /api/project-templates handler with a real plugin handler over a real
// installed-plugins store, so what is asserted here is exactly the JSON a
// browser receives.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// catalogEndpointServer wires a server whose templates library and installed
// plugin store are both real files on disk.
func catalogEndpointServer(t *testing.T, libDir, pluginsDir string, installed []plugin.InstalledPlugin) *Server {
	t.Helper()
	configMgr := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configMgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := configMgr.SetTemplatesRoot(libDir); err != nil {
		t.Fatal(err)
	}
	if installed != nil {
		data, err := json.MarshalIndent(installed, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pluginsDir, "installed.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{}
	s.Core = NewCoreSystemFacade(nil, nil, configMgr, nil, nil)
	s.Handlers = &HandlerFacade{
		Plugin: pluginhttp.NewHandler(nil, nil, t.TempDir(), pluginsDir),
	}
	return s
}

type catalogResponse struct {
	Templates []struct {
		ID          string                       `json:"id"`
		Name        string                       `json:"name"`
		Builtin     bool                         `json:"builtin"`
		HasSkeleton bool                         `json:"has_skeleton"`
		Readiness   blueprintreadiness.Readiness `json:"readiness"`
	} `json:"templates"`
	TemplatesRoot string `json:"templates_root"`
}

func getCatalog(t *testing.T, s *Server) (catalogResponse, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleProjectTemplates(w, httptest.NewRequest(http.MethodGet, "/api/project-templates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response catalogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	return response, w.Body.String()
}

func endpointPluginRecord(t *testing.T, name, blueprintID string, enabled bool, artifacts []plugin.ResolvedArtifact) plugin.InstalledPlugin {
	t.Helper()
	owner := &workspace.PluginTemplateOwner{
		PluginID: name, PluginVersion: "2.1.0", BlueprintID: blueprintID, BlueprintVersion: 1,
	}
	qualified := "plugin:" + name + ":" + blueprintID
	return plugin.InstalledPlugin{
		Name: name, Version: "2.1.0", Enabled: enabled, Generation: 9,
		Source:     "https://example.test/" + name + ".git",
		InstallDir: filepath.Join(t.TempDir(), "install-root"),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: name, Version: "2.1.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedArtifacts: artifacts,
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: blueprintID, QualifiedID: qualified, Version: 1,
			SkeletonRoot: filepath.Join(t.TempDir(), "skeleton"),
			Template: projecttemplates.Template{
				ID: qualified, Name: "Plugin " + blueprintID, PluginOwner: owner,
			},
		}},
	}
}

func TestProjectTemplatesEndpointCarriesReadinessForEveryState(t *testing.T) {
	if projecttemplates.IsBuiltinStarterID("song-production") {
		t.Skip("embedded starter catalog now ships this ID")
	}
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "research-project", `{
		"name":"Research Project","builtin":true,"builtin_version":1,"agents":[{"name":"Researcher"}]
	}`)
	writeMatrixTemplate(t, libDir, "my-notes", `{"name":"My Notes","agents":[{"name":"Lead"}]}`)
	writeMatrixTemplate(t, libDir, "broken-user-template", `{
		"name":"Broken","agents":[{"name":"Lead"}],
		"setup_wizard":{"version":1,"title":"S","steps":[
			{"id":"r","kind":"readiness","adapter":"not_a_real_adapter","required":true}]}
	}`)
	writeMatrixTemplate(t, libDir, "song-production", `{
		"name":"Song Production","builtin":true,"builtin_version":3,"agents":[{"name":"Producer"}]
	}`)
	writeMatrixTemplate(t, libDir, "needs-plugin", `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["absent-plugin"],"plugin_sources":{"absent-plugin":"https://example.test/absent.git"}}
	}`)

	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	installed := []plugin.InstalledPlugin{
		endpointPluginRecord(t, "enabled-owner", "starter", true, availableArtifacts()),
		endpointPluginRecord(t, "disabled-owner", "disabled-blueprint", false, availableArtifacts()),
		endpointPluginRecord(t, "unsupported-owner", "unsupported-blueprint", true, unsupportedArtifacts()),
	}
	s := catalogEndpointServer(t, libDir, pluginsDir, installed)

	response, raw := getCatalog(t, s)
	byID := make(map[string]blueprintreadiness.Readiness, len(response.Templates))
	for _, entry := range response.Templates {
		byID[entry.ID] = entry.Readiness
	}

	for _, tc := range []struct {
		id        string
		state     blueprintreadiness.State
		ownership blueprintreadiness.Ownership
		reason    blueprintreadiness.Reason
	}{
		{"research-project", blueprintreadiness.StateReady, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonNone},
		{"my-notes", blueprintreadiness.StateReady, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonNone},
		{"broken-user-template", blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonManifestInvalid},
		{"song-production", blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonBlueprintRetired},
		{"needs-plugin", blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonPluginInstallRequired},
		{"plugin:enabled-owner:starter", blueprintreadiness.StateReady, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonNone},
		{"plugin:disabled-owner:disabled-blueprint", blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPluginEnableRequired},
		{"plugin:unsupported-owner:unsupported-blueprint", blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPlatformUnsupported},
	} {
		readiness, present := byID[tc.id]
		if !present {
			t.Errorf("%s missing from the endpoint response", tc.id)
			continue
		}
		if readiness.State != tc.state || readiness.Ownership != tc.ownership || readiness.Reason != tc.reason {
			t.Errorf("%s = {%q %q %q}, want {%q %q %q}", tc.id,
				readiness.State, readiness.Ownership, readiness.Reason, tc.state, tc.ownership, tc.reason)
		}
		if readiness.State != blueprintreadiness.StateReady && len(readiness.Actions) == 0 {
			t.Errorf("%s is blocked but offers no way forward", tc.id)
		}
		for _, action := range readiness.Actions {
			if _, ok := blueprintreadiness.ParseAction(string(action)); !ok {
				t.Errorf("%s offers an action outside the allowlist: %q", tc.id, action)
			}
		}
	}

	if response.TemplatesRoot != libDir {
		t.Errorf("templates_root = %q, want %q", response.TemplatesRoot, libDir)
	}
	// The existing template fields a client already reads are untouched.
	for _, entry := range response.Templates {
		if entry.Name == "" {
			t.Errorf("%s lost its display name", entry.ID)
		}
	}
	_ = raw
}

// TestProjectTemplatesEndpointDisclosesNoLocalDetail is the response-level
// disclosure guarantee. Install roots, skeleton roots, artifact locations, and
// the sources a manifest declared are all present in the state the handler
// reads from; none of them may appear in what it writes.
func TestProjectTemplatesEndpointDisclosesNoLocalDetail(t *testing.T) {
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "hostile", `{
		"name":"Hostile","agents":[{"name":"Lead"}],
		"tools":{"plugins":["absent-plugin"],"plugin_sources":{"absent-plugin":"https://evil.test/absent.git"}}
	}`)

	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	installed := []plugin.InstalledPlugin{
		endpointPluginRecord(t, "disabled-owner", "starter", false, availableArtifacts()),
	}
	installed[0].InstallDir = "/Users/someone/Library/Application Support/ori/plugins/disabled-owner"
	installed[0].ResolvedArtifacts[0].ManagedPath = "/Users/someone/Library/ori/artifacts/svc"
	installed[0].ResolvedBlueprints[0].SkeletonRoot = "/Users/someone/Library/ori/plugins/disabled-owner/blueprints/starter/project"
	s := catalogEndpointServer(t, libDir, pluginsDir, installed)

	response, _ := getCatalog(t, s)
	for _, entry := range response.Templates {
		encoded, err := json.Marshal(entry.Readiness)
		if err != nil {
			t.Fatal(err)
		}
		body := string(encoded)
		for _, forbidden := range []string{
			"://", "/Users/", "Application Support", "evil.test", "artifacts", "skeleton", "installed.json",
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s readiness discloses %q: %s", entry.ID, forbidden, body)
			}
		}
	}

	// An inert candidate must also be uninstantiable in what it advertises.
	for _, entry := range response.Templates {
		if strings.HasPrefix(entry.ID, "plugin:") && entry.Readiness.State != blueprintreadiness.StateReady && entry.HasSkeleton {
			t.Errorf("%s advertises a skeleton while it cannot be created", entry.ID)
		}
	}
}

// TestProjectTemplatesEndpointReportsAnUnreadableInstalledStore proves the
// handler distinguishes "nothing installed" from "could not look".
func TestProjectTemplatesEndpointReportsAnUnreadableInstalledStore(t *testing.T) {
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "needs-plugin", `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["owner-plugin"]}
	}`)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// A corrupt registry: present, but not parseable.
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := catalogEndpointServer(t, libDir, pluginsDir, nil)

	response, raw := getCatalog(t, s)
	if len(response.Templates) != 1 {
		t.Fatalf("catalog = %+v", response.Templates)
	}
	readiness := response.Templates[0].Readiness
	if readiness.Reason != blueprintreadiness.ReasonDependencyStateUnknown {
		t.Fatalf("an unreadable store was reported as %q: %s", readiness.Reason, raw)
	}
	if readiness.Creatable() {
		t.Fatal("a blueprint was offered as ready while its dependencies could not be checked")
	}
	// The parse failure itself must not reach the browser.
	if strings.Contains(raw, "installed.json") || strings.Contains(raw, "not json") {
		t.Fatalf("the store failure leaked into the response: %s", raw)
	}
}
