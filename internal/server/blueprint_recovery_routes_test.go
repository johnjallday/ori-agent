package server

// In-wizard recovery, at the endpoint.
//
// The security properties this feature depends on all live here: the client
// names an action and a plugin, never a source; nothing happens without a
// confirmation; a confirmation given against one set of components cannot be
// applied to another; and a half-finished recovery is reported as exactly
// that.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func postRecovery(t *testing.T, s *Server, templateID string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/project-templates/"+templateID+"/plugin-recovery", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("templateID", templateID)
	w := httptest.NewRecorder()
	s.handleBlueprintPluginRecovery(w, req)

	var decoded map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode (%d): %v: %s", w.Code, err, w.Body.String())
		}
	}
	return w, decoded
}

// recoveryServer wires a server whose templates library and installed-plugin
// store are both real files, with one blueprint declaring one plugin.
func recoveryServer(t *testing.T, manifest string, installed []plugin.InstalledPlugin) (*Server, string) {
	t.Helper()
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "needs-plugin", manifest)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	return catalogEndpointServer(t, libDir, pluginsDir, installed), libDir
}

const recoveryManifestWithSource = `{
	"name":"Needs Plugin","agents":[{"name":"Lead"}],
	"tools":{"plugins":["owner-plugin"],"plugin_sources":{"owner-plugin":"https://example.test/owner.git"}}
}`

func TestBlueprintRecoveryRefusesUnknownAndNonServerActions(t *testing.T) {
	s, _ := recoveryServer(t, recoveryManifestWithSource, nil)

	for _, action := range []string{
		`"run_shell"`, `"install_plugin;rm -rf /"`, `"INSTALL_PLUGIN"`, `" install_plugin"`, `""`,
	} {
		w, _ := postRecovery(t, s, "needs-plugin",
			`{"action":`+action+`,"plugin":"owner-plugin","confirm":true}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("action %s = %d, want 400: %s", action, w.Code, w.Body.String())
		}
	}

	// Routing actions are the client's to perform. The server refusing them is
	// what keeps "the server only ever does the three lifecycle operations"
	// true by construction rather than by convention.
	for _, action := range []string{"manage_plugins", "change_blueprint", "edit_template_manifest", "retry"} {
		w, _ := postRecovery(t, s, "needs-plugin",
			`{"action":"`+action+`","plugin":"owner-plugin","confirm":true}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("routing action %q = %d, want 400: %s", action, w.Code, w.Body.String())
		}
	}
}

// The blueprint is the authority on which plugin it needs. Without this, the
// wizard would install anything a client could name.
func TestBlueprintRecoveryRefusesAPluginTheBlueprintDoesNotDeclare(t *testing.T) {
	s, _ := recoveryServer(t, recoveryManifestWithSource, nil)

	for _, name := range []string{"some-other-plugin", "", "  "} {
		w, _ := postRecovery(t, s, "needs-plugin",
			`{"action":"install_plugin","plugin":"`+name+`","confirm":true}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("plugin %q = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}

	// The declared one is accepted (case-insensitively) and gets as far as
	// trying to resolve its source.
	w, _ := postRecovery(t, s, "needs-plugin",
		`{"action":"install_plugin","plugin":"Owner-Plugin","confirm":false}`)
	if w.Code == http.StatusBadRequest {
		t.Fatalf("the declared plugin was rejected: %s", w.Body.String())
	}
}

func TestBlueprintRecoveryWithoutADeclaredSourceRoutesToPlugins(t *testing.T) {
	// Same blueprint, no plugin_sources entry: nothing to install from that the
	// user has been shown.
	s, _ := recoveryServer(t, `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["owner-plugin"]}
	}`, nil)

	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"install_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	if outcome == nil {
		t.Fatalf("no outcome reported: %v", resp)
	}
	if completed, _ := outcome["completed"].(bool); completed {
		t.Fatal("a refused install reported completion")
	}
	summary, _ := outcome["summary"].(string)
	if !strings.Contains(summary, "does not say where") {
		t.Fatalf("unhelpful summary: %q", summary)
	}
	// Guessing a marketplace entry from the plugin's name would be the wrong
	// kind of helpful: the user must see a source before it is acted on.
	if strings.Contains(w.Body.String(), "://") {
		t.Fatalf("the refusal disclosed a locator: %s", w.Body.String())
	}
}

// A confirmation is consent for the components disclosed at preview time. If
// the plugin changed in between, that consent no longer describes anything.
func TestBlueprintRecoveryRefusesAStaleConfirmation(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	installed.Generation = 9
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true,"generation":4}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("a stale confirmation was applied: %d %s", w.Code, w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	summary, _ := outcome["summary"].(string)
	if !strings.Contains(summary, "changed while you were reviewing") {
		t.Fatalf("unclear stale-confirmation copy: %q", summary)
	}
	// Refusing must not have enabled it.
	readiness, _ := resp["readiness"].(map[string]any)
	if state, _ := readiness["state"].(string); state == "ready" {
		t.Fatalf("a refused confirmation still changed the plugin: %v", readiness)
	}
}

func TestBlueprintRecoveryMatchingGenerationIsApplied(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	installed.Generation = 9
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true,"generation":9}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	if completed, _ := outcome["completed"].(bool); !completed {
		t.Fatalf("enable did not report completion: %v", outcome)
	}
	readiness, _ := resp["readiness"].(map[string]any)
	if state, _ := readiness["state"].(string); state != "ready" {
		t.Fatalf("readiness after enable = %v, want ready", readiness)
	}
}

// A generation of 0 means "the client did not have one" — an older client, or
// a plugin that is not installed yet and therefore has no generation to send.
func TestBlueprintRecoveryTreatsAbsentGenerationAsUnchecked(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	installed.Generation = 9
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	w, _ := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBlueprintRecoveryEnableFailureLeavesThePluginInstalledAndDisabled(t *testing.T) {
	// No such plugin in the store, so SetEnabled fails.
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{})

	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	if completed, _ := outcome["completed"].(bool); completed {
		t.Fatal("a failed enable reported completion")
	}
	detail, _ := outcome["detail"].(string)
	if !strings.Contains(detail, "still installed and still disabled") {
		t.Fatalf("the failure does not say where the plugin was left: %q", detail)
	}
	steps, _ := outcome["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("expected one recorded step, got %v", steps)
	}
	step, _ := steps[0].(map[string]any)
	if name, _ := step["name"].(string); name != "enable" {
		t.Fatalf("wrong step recorded: %v", step)
	}
	if succeeded, _ := step["succeeded"].(bool); succeeded {
		t.Fatalf("a failed step was recorded as succeeded: %v", step)
	}
}

// A preview must change nothing. This is the property the whole confirm gate
// rests on: the user can look without committing.
func TestBlueprintRecoveryPreviewChangesNothing(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	before, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	postRecovery(t, s, "needs-plugin",
		`{"action":"review_plugin_update","plugin":"owner-plugin","confirm":false}`)

	after, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("preview changed the store: %d -> %d", len(before), len(after))
	}
	if before[0].Enabled != after[0].Enabled || before[0].Generation != after[0].Generation {
		t.Fatalf("preview mutated the plugin: %+v -> %+v", before[0], after[0])
	}
}

func TestBlueprintRecoveryRespondsWithCurrentReadinessAndBlueprintID(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	_, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)

	// The client is told which entry to select rather than matching display
	// text, and what the state now is rather than inferring it from a 200.
	if id, _ := resp["blueprint_id"].(string); id != "needs-plugin" {
		t.Fatalf("blueprint_id = %q, want the blueprint acted on", id)
	}
	readiness, _ := resp["readiness"].(map[string]any)
	if readiness == nil {
		t.Fatalf("no readiness in the response: %v", resp)
	}
	if _, ok := blueprintreadiness.ParseAction(""); ok {
		t.Fatal("the empty action is on the allowlist")
	}
}

// A plugin-owned blueprint recovers through its owner even while that owner is
// disabled — which is exactly the state the recovery exists to leave.
func TestBlueprintRecoveryEnablesAPluginOwnedBlueprintsOwner(t *testing.T) {
	disabled := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	libDir := t.TempDir()
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	s := catalogEndpointServer(t, libDir, pluginsDir, []plugin.InstalledPlugin{disabled})

	w, resp := postRecovery(t, s, "plugin:owner-plugin:starter",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	readiness, _ := resp["readiness"].(map[string]any)
	if state, _ := readiness["state"].(string); state != "ready" {
		t.Fatalf("the blueprint is still not ready: %v", readiness)
	}
	if id, _ := resp["blueprint_id"].(string); id != "plugin:owner-plugin:starter" {
		t.Fatalf("blueprint_id = %q", id)
	}
}

func TestBlueprintRecoveryOnAnUnknownBlueprintIsNotFound(t *testing.T) {
	s, _ := recoveryServer(t, recoveryManifestWithSource, nil)
	for _, id := range []string{"no-such-template", "plugin:ghost:starter"} {
		w, _ := postRecovery(t, s, id,
			`{"action":"install_plugin","plugin":"owner-plugin","confirm":false}`)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404: %s", id, w.Code, w.Body.String())
		}
	}
}

// Every response the endpoint can produce stays inside the public contract.
func TestBlueprintRecoveryDisclosesNoLocalDetail(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	installed.InstallDir = "/Users/someone/Library/Application Support/ori/plugins/owner-plugin"
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	for _, body := range []string{
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`,
		`{"action":"review_plugin_update","plugin":"owner-plugin","confirm":false}`,
		`{"action":"install_plugin","plugin":"owner-plugin","confirm":false}`,
	} {
		w, resp := postRecovery(t, s, "needs-plugin", body)
		// The trust report is exempt: disclosing the command lines is its whole
		// purpose. Everything else must stay clean.
		delete(resp, "trust")
		encoded, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/Users/", "Application Support", "example.test"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Errorf("%s (%d) discloses %q: %s", body, w.Code, forbidden, encoded)
			}
		}
	}
}

// The source is disclosed with the trust preview and nowhere else. Both halves
// matter: withholding it there would ask the user to trust software without
// saying where it came from, and echoing it anywhere else would let a manifest
// put a URL of its choosing in front of them with no context.
func TestBlueprintRecoveryDisclosesTheSourceOnlyWithATrustPreview(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	// An applied action carries no trust report, so it carries no source.
	_, applied := postRecovery(t, s, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)
	if source, present := applied["source"]; present {
		t.Fatalf("an applied action disclosed a source: %v", source)
	}

	// Nor does any readiness projection in the catalog.
	//
	// Scope note: the catalog still serializes each template's own
	// `tools.plugin_sources`, which is the long-standing public template
	// contract the authoring UI reads. That is a separate, pre-existing
	// exposure of a manifest's own declaration — not a recovery descriptor.
	// What this feature guarantees is narrower and is what is asserted here:
	// the readiness projection reports only that a source EXISTS, so a card,
	// a badge, or a refusal can never put a manifest-supplied URL in front of
	// the user outside the trust preview.
	response, _ := getCatalog(t, s)
	if len(response.Templates) == 0 {
		t.Fatal("catalog was empty")
	}
	for _, entry := range response.Templates {
		encoded, err := json.Marshal(entry.Readiness)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "example.test") {
			t.Errorf("%s readiness echoed a template-declared source: %s", entry.ID, encoded)
		}
		if entry.ID == "needs-plugin" && !entry.Readiness.Creatable() {
			// It declares a source, and the projection says so without saying what.
			if entry.Readiness.Dependency == nil || !entry.Readiness.Dependency.SourceDeclared {
				t.Errorf("a declared source was not reported as available: %+v", entry.Readiness)
			}
		}
	}
}

func TestRecoveryResponseDropsASourceWithoutADisclosure(t *testing.T) {
	// The pairing is enforced in one place rather than trusted at every call
	// site: no trust report, no source.
	s, _ := recoveryServer(t, recoveryManifestWithSource, nil)
	w := httptest.NewRecorder()
	s.respondRecoveryWithSource(w, http.StatusOK, projecttemplates.Template{ID: "x"},
		nil, nil, false, "https://example.test/owner.git")
	if strings.Contains(w.Body.String(), "example.test") {
		t.Fatalf("a source travelled without a disclosure: %s", w.Body.String())
	}
}

func TestRecoveryFailureMessageRedactsLocators(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"declined", plugin.ErrInstallDeclined, "You cancelled before anything was changed."},
		{"unsupported", plugin.ErrArtifactUnsupported, "This plugin ships nothing that runs on this computer."},
		{"invalid", plugin.ErrArtifactInvalid, "The downloaded files did not match what the plugin published."},
	} {
		if got := recoveryFailureMessage(tc.err); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if recoveryFailureMessage(nil) != "" {
		t.Error("a nil error produced a message")
	}
	// An arbitrary manager error never reaches display copy at all. Redaction
	// is not enough here: `Could not resolve host: secret.test` is a bare
	// hostname that no locator heuristic catches, and it is exactly the
	// template-supplied source the trust preview is meant to be the only
	// discloser of.
	for _, raw := range []string{
		"plugin: clone https://secret.test/x.git into /Users/me/plugins failed",
		"plugin: git clone failed: exit status 128: Could not resolve host: secret.test",
		"plugin: exec /usr/local/bin/thing --token abc123: permission denied",
	} {
		got := recoveryFailureMessage(&pathError{raw})
		if strings.Contains(got, "secret.test") || strings.Contains(got, "/Users/") ||
			strings.Contains(got, "abc123") || strings.Contains(got, "/usr/local") {
			t.Errorf("a raw manager error reached display copy: %q", got)
		}
		if got == "" {
			t.Errorf("a failure produced no message at all for %q", raw)
		}
	}
}

type pathError struct{ message string }

func (e *pathError) Error() string { return e.message }

// blueprintDeclaresPlugin is the guard every request passes through, so its
// edges are worth stating directly.
func TestBlueprintDeclaresPlugin(t *testing.T) {
	declared := projecttemplates.Template{
		Tools: projecttemplates.ToolDefaults{Plugins: []string{"Owner-Plugin"}},
	}
	owned := projecttemplates.Template{
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "owner-plugin", BlueprintID: "starter"},
	}
	for _, tc := range []struct {
		name     string
		template projecttemplates.Template
		plugin   string
		want     bool
	}{
		{"declared exactly", declared, "Owner-Plugin", true},
		{"declared, different case", declared, "owner-plugin", true},
		{"declared, padded", declared, "  owner-plugin  ", true},
		{"not declared", declared, "other", false},
		{"empty", declared, "", false},
		{"contributed by its owner", owned, "owner-plugin", true},
		{"a different plugin than its owner", owned, "other", false},
	} {
		if got := blueprintDeclaresPlugin(tc.template, tc.plugin); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeclaredPluginSourceIsReadServerSideOnly(t *testing.T) {
	template := projecttemplates.Template{
		Tools: projecttemplates.ToolDefaults{
			Plugins:       []string{"owner-plugin"},
			PluginSources: map[string]string{"Owner-Plugin": "https://example.test/owner.git"},
		},
	}
	if got := declaredPluginSource(template, "owner-plugin"); got != "https://example.test/owner.git" {
		t.Fatalf("source = %q", got)
	}
	if got := declaredPluginSource(template, "other"); got != "" {
		t.Fatalf("an undeclared plugin resolved a source: %q", got)
	}
}

func TestRecoveryOutcomeNormalizationIsHonest(t *testing.T) {
	// Completed cannot be asserted independently of the steps: a partial
	// recovery that claims success is the one lie this type exists to prevent.
	partial := blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionInstallPlugin, Completed: true,
		Steps: []blueprintreadiness.OutcomeStep{
			{Name: blueprintreadiness.StepInstall, Succeeded: true},
			{Name: blueprintreadiness.StepEnable, Succeeded: false, Message: "could not start /usr/local/bin/thing"},
		},
	}.Normalize()
	if partial.Completed {
		t.Fatal("an outcome with a failed step reported completion")
	}
	if strings.Contains(partial.Steps[1].Message, "/usr/local") {
		t.Fatalf("a step message leaked a path: %q", partial.Steps[1].Message)
	}
	if !partial.Succeeded(blueprintreadiness.StepInstall) {
		t.Fatal("the successful step was lost")
	}
	if partial.Succeeded(blueprintreadiness.StepEnable) {
		t.Fatal("the failed step reported success")
	}

	// An outcome that recorded nothing did nothing.
	empty := blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionEnablePlugin, Completed: true,
	}.Normalize()
	if empty.Completed {
		t.Fatal("an outcome with no steps reported completion")
	}

	// Unknown step names are dropped rather than rendered.
	unknown := blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionEnablePlugin,
		Steps:  []blueprintreadiness.OutcomeStep{{Name: "exfiltrate", Succeeded: true}},
	}.Normalize()
	if len(unknown.Steps) != 0 {
		t.Fatalf("an unknown step survived: %+v", unknown.Steps)
	}
}

// The endpoint refuses to act when plugin management is not wired at all,
// rather than reporting a success nothing performed.
func TestBlueprintRecoveryWithoutAPluginSubsystemIsUnavailable(t *testing.T) {
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "needs-plugin", recoveryManifestWithSource)
	s := newTemplateRoutesServer(t, libDir)

	w, _ := postRecovery(t, s, "needs-plugin",
		`{"action":"install_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

// writeRecoveryFixtureFile keeps the fixture helper honest about the library
// it writes into: a recovery test that silently wrote nowhere would pass for
// the wrong reason.
func TestRecoveryFixtureWritesIntoTheLibrary(t *testing.T) {
	_, libDir := recoveryServer(t, recoveryManifestWithSource, nil)
	if _, err := os.Stat(filepath.Join(libDir, "needs-plugin", projecttemplates.ManifestFileName)); err != nil {
		t.Fatalf("fixture manifest missing: %v", err)
	}
}
