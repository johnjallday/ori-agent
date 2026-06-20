package pluginhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

type stubReg struct{ added map[string]mcp.ServerConfig }

func (s *stubReg) AddServer(c mcp.ServerConfig) error {
	if s.added == nil {
		s.added = map[string]mcp.ServerConfig{}
	}
	s.added[c.Name] = c
	return nil
}
func (s *stubReg) RemoveServer(name string) error { delete(s.added, name); return nil }

type stubSkills struct{}

func (stubSkills) InstallSkill(_, _, _ string) error { return nil }
func (stubSkills) RemoveSkill(_, _ string) error     { return nil }

func testHandler(t *testing.T) *Handler {
	t.Helper()
	mgr := plugin.NewManager(&stubReg{}, stubSkills{}, t.TempDir(), "")
	return newHandlerWithManager(mgr)
}

func claudeBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.1.0"}`)
	mustWrite(t, filepath.Join(root, ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"}}`)
	mustWrite(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\n---\n")
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func postInstall(t *testing.T, h *Handler, source string, confirm bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"source":"` + source + `","confirm":` + map[bool]string{true: "true", false: "false"}[confirm] + `}`
	rr := httptest.NewRecorder()
	h.InstallHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(body)))
	return rr
}

func listBody(t *testing.T, h *Handler) string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ListHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins", nil))
	return rr.Body.String()
}

func TestInstallPreviewMakesNoChanges(t *testing.T) {
	h := testHandler(t)
	rr := postInstall(t, h, claudeBundle(t), false)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"installed":false`) {
		t.Errorf("expected installed:false, got %s", rr.Body.String())
	}
	if strings.Contains(listBody(t, h), "reaper") {
		t.Error("preview must not record a plugin")
	}
}

func TestInstallConfirmThenList(t *testing.T) {
	h := testHandler(t)
	rr := postInstall(t, h, claudeBundle(t), true)
	if rr.Code != http.StatusOK {
		t.Fatalf("install status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"installed":true`) {
		t.Errorf("expected installed:true, got %s", rr.Body.String())
	}
	if !strings.Contains(listBody(t, h), "reaper") {
		t.Errorf("expected reaper in list, got %s", listBody(t, h))
	}
}

func TestMarketplacesOfficialStatus(t *testing.T) {
	h := testHandler(t)

	getMarketplaces := func() string {
		t.Helper()
		rr := httptest.NewRecorder()
		h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/marketplaces", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET marketplaces: %d %s", rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}

	// Before adding: official block present, source exposed, added=false.
	body := getMarketplaces()
	if !strings.Contains(body, plugin.OfficialMarketplaceSource) {
		t.Errorf("official source not exposed: %s", body)
	}
	if !strings.Contains(body, `"added":false`) {
		t.Errorf("expected added:false, got %s", body)
	}

	// Add a local catalog whose name matches the official marketplace name; the
	// status must flip to added=true (idempotent add keyed by name).
	catalog := t.TempDir()
	mustWrite(t, filepath.Join(catalog, "marketplace.json"),
		`{"name":"`+plugin.OfficialMarketplaceName+`","plugins":[]}`)
	addBody := `{"source":"` + catalog + `"}`
	rr := httptest.NewRecorder()
	h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces", strings.NewReader(addBody)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST marketplace: %d %s", rr.Code, rr.Body.String())
	}
	// Re-add to prove idempotency (no duplicate record).
	rr = httptest.NewRecorder()
	h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces", strings.NewReader(addBody)))
	if rr.Code != http.StatusOK {
		t.Fatalf("re-add marketplace: %d %s", rr.Code, rr.Body.String())
	}

	// Each added marketplace record carries a "dir" field (the official status
	// block does not), so this counts records — proving the re-add didn't dupe.
	if got := strings.Count(getMarketplaces(), `"dir":`); got != 1 {
		t.Errorf("expected exactly 1 marketplace record, got %d: %s", got, getMarketplaces())
	}
	if !strings.Contains(getMarketplaces(), `"added":true`) {
		t.Errorf("expected added:true after add, got %s", getMarketplaces())
	}
}

func TestUninstall(t *testing.T) {
	h := testHandler(t)
	if rr := postInstall(t, h, claudeBundle(t), true); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/plugins/reaper", nil)
	req.SetPathValue("name", "reaper")
	rr := httptest.NewRecorder()
	h.UninstallHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("uninstall: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(listBody(t, h), "reaper") {
		t.Error("reaper should be gone after uninstall")
	}
}
