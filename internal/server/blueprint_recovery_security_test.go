package server

// Negative security tests for in-wizard plugin recovery.
//
// Each case tampers with an otherwise-real, otherwise-valid plugin bundle
// (the repository's example workspace-surface-demo plugin, copied and then
// modified) so the low-level protections these rely on — skeleton symlink
// rejection, artifact digest verification — are exercised for real, through
// the actual recovery endpoint, rather than only unit-tested in isolation at
// the plugin package.

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// copySecurityFixture copies a directory tree so a known-valid bundle can be
// tampered with per test without touching the checked-in example.
func copySecurityFixture(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- fixture path under the repo's examples tree
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func exampleSurfacePluginRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "workspace-surface-demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("repository example plugin not present at %s: %v", root, err)
	}
	return root
}

// pluginRecoveryTestServer wires a server with a real Manager and one
// blueprint declaring a dependency on the named plugin, resolved to source.
func pluginRecoveryTestServer(t *testing.T, pluginName, source string) *Server {
	t.Helper()
	libDir := t.TempDir()
	manifest := `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["` + pluginName + `"],"plugin_sources":{"` + pluginName + `":"` + strings.ReplaceAll(source, `\`, `\\`) + `"}}
	}`
	writeMatrixTemplate(t, libDir, "needs-plugin", manifest)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	return catalogEndpointServer(t, libDir, pluginsDir, nil)
}

// TestBlueprintRecoveryRefusesASymlinkedSkeleton proves a symlink smuggled
// into a plugin's blueprint skeleton is refused at install time, through the
// real recovery endpoint — cleanly, with no path disclosed and nothing
// partially registered.
func TestBlueprintRecoveryRefusesASymlinkedSkeleton(t *testing.T) {
	source := exampleSurfacePluginRoot(t)
	tampered := t.TempDir()
	copySecurityFixture(t, source, tampered)

	target := filepath.Join(tampered, "sensitive-target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(tampered, "blueprints", "demo-workspace", "project", "escape")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}

	s := pluginRecoveryTestServer(t, "workspace-surface-demo", tampered)
	before, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}

	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"install_plugin","plugin":"workspace-surface-demo","confirm":true}`)
	if w.Code == http.StatusOK {
		t.Fatalf("a symlinked skeleton was accepted: %s", w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	if completed, _ := outcome["completed"].(bool); completed {
		t.Fatal("a refused install reported completion")
	}
	body, _ := json.Marshal(resp)
	if strings.Contains(string(body), tampered) || strings.Contains(string(body), "sensitive-target") {
		t.Fatalf("the refusal disclosed a local path: %s", body)
	}

	after, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a refused install still registered something: before=%d after=%d", len(before), len(after))
	}
}

// TestBlueprintRecoveryRefusesAnArtifactThatFailsDigestVerification proves a
// bundled artifact whose bytes do not match its declared digest is refused,
// with the failure reported as the generic sentinel rather than a raw
// checksum mismatch that could hint at the expected/actual bytes.
func TestBlueprintRecoveryRefusesAnArtifactThatFailsDigestVerification(t *testing.T) {
	source := exampleSurfacePluginRoot(t)
	tampered := t.TempDir()
	copySecurityFixture(t, source, tampered)

	manifestPath := filepath.Join(tampered, ".ori-plugin", "plugin.json")
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- tampered is this test's own temp copy
	if err != nil {
		t.Fatal(err)
	}
	const validDigest = "7b93ae6f9a81853386f0e04c8db0f7be8ccc61e2d9b3957dc752d9dc2311ffba"
	if !strings.Contains(string(data), validDigest) {
		t.Fatalf("fixture no longer contains the digest this test tampers with; update it")
	}
	tamperedDigest := strings.Repeat("0", len(validDigest))
	corrupted := strings.ReplaceAll(string(data), validDigest, tamperedDigest)
	if err := os.WriteFile(manifestPath, []byte(corrupted), 0o640); err != nil { // #nosec G306 -- matches the example fixture's own file mode
		t.Fatal(err)
	}

	s := pluginRecoveryTestServer(t, "workspace-surface-demo", tampered)
	w, resp := postRecovery(t, s, "needs-plugin",
		`{"action":"install_plugin","plugin":"workspace-surface-demo","confirm":true}`)
	if w.Code == http.StatusOK {
		t.Fatalf("an artifact with a mismatched digest was accepted: %s", w.Body.String())
	}
	outcome, _ := resp["outcome"].(map[string]any)
	if completed, _ := outcome["completed"].(bool); completed {
		t.Fatal("a digest-verification failure reported completion")
	}
	steps, _ := outcome["steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("no step recorded for the failed install")
	}
	step, _ := steps[0].(map[string]any)
	message, _ := step["message"].(string)
	// The sentinel wording, not a raw checksum comparison that could leak
	// which half of the digest was expected.
	if !strings.Contains(message, "did not match what the plugin published") {
		t.Fatalf("unexpected failure message: %q", message)
	}
	if strings.Contains(message, validDigest) || strings.Contains(message, tamperedDigest) {
		t.Fatalf("a raw digest reached display copy: %q", message)
	}
}

// TestBlueprintRecoveryRefusesPathTraversalInTheTemplateID proves the
// endpoint cannot be pointed outside the templates library by a crafted
// path segment.
func TestBlueprintRecoveryRefusesPathTraversalInTheTemplateID(t *testing.T) {
	s, _ := recoveryServer(t, recoveryManifestWithSource, nil)

	for _, id := range []string{
		"../../../../etc/passwd",
		"..%2f..%2fetc%2fpasswd",
		"needs-plugin/../../../etc",
		".",
		"..",
	} {
		w, _ := postRecovery(t, s, id,
			`{"action":"install_plugin","plugin":"owner-plugin","confirm":false}`)
		if w.Code == http.StatusOK {
			t.Errorf("path-traversal templateID %q was accepted", id)
		}
	}
}

// TestBlueprintRecoveryRefusesAReplayedConfirmationAfterSuccess proves a
// captured confirm request cannot be resent after the state it was consented
// to has already moved on — the stale-generation guard applied against a real
// mutation, not a hand-crafted mismatch.
func TestBlueprintRecoveryRefusesAReplayedConfirmationAfterSuccess(t *testing.T) {
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())
	installed.Generation = 5
	s, _ := recoveryServer(t, recoveryManifestWithSource, []plugin.InstalledPlugin{installed})

	captured := `{"action":"enable_plugin","plugin":"owner-plugin","confirm":true,"generation":5}`
	w, resp := postRecovery(t, s, "needs-plugin", captured)
	if w.Code != http.StatusOK {
		t.Fatalf("the original confirmation failed: %d %s", w.Code, w.Body.String())
	}
	if outcome, _ := resp["outcome"].(map[string]any); outcome == nil {
		t.Fatalf("no outcome for the original confirmation: %v", resp)
	}

	// Replay the exact same captured body. The plugin's generation has moved
	// on (SetEnabled bumps it), so this must be refused, not silently re-run.
	w2, resp2 := postRecovery(t, s, "needs-plugin", captured)
	if w2.Code != http.StatusConflict {
		t.Fatalf("a replayed confirmation was applied: %d %s", w2.Code, w2.Body.String())
	}
	outcome2, _ := resp2["outcome"].(map[string]any)
	summary2, _ := outcome2["summary"].(string)
	if !strings.Contains(summary2, "changed while you were reviewing") {
		t.Fatalf("unclear replay-refusal copy: %q", summary2)
	}
}

// TestBlueprintRecoveryPluginScopingHasNoWorkspaceOrUserParameter documents,
// rather than merely asserts, why "cross-user/cross-workspace access" is not
// a meaningful axis for this endpoint: Ori is a single-tenant desktop
// application, and installed plugins are a host-global resource with no
// per-user or per-workspace owner to leak across. The route carries no
// workspace or user identifier, so there is no such scope to check — verified
// here by round-tripping the same plugin store from two independently built
// server instances and confirming both see (and can act on) the same global
// state, which is the intended behavior for a shared host-level resource.
func TestBlueprintRecoveryPluginScopingHasNoWorkspaceOrUserParameter(t *testing.T) {
	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, "needs-plugin", recoveryManifestWithSource)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	installed := endpointPluginRecord(t, "owner-plugin", "starter", false, availableArtifacts())

	first := catalogEndpointServer(t, libDir, pluginsDir, []plugin.InstalledPlugin{installed})
	w, _ := postRecovery(t, first, "needs-plugin",
		`{"action":"enable_plugin","plugin":"owner-plugin","confirm":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("enable via the first instance failed: %d %s", w.Code, w.Body.String())
	}

	// A second server instance over the SAME on-disk store — modeling any
	// other process/session on this machine — sees the change. There is no
	// per-instance isolation to defeat, because none is claimed.
	second := catalogEndpointServer(t, libDir, pluginsDir, nil)
	list, err := second.Handlers.Plugin.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("the second instance did not see the shared host-level state: %+v", list)
	}
}
